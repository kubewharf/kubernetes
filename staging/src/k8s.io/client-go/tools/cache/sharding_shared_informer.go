/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cache

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/clock"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache/sharding"
	"k8s.io/klog"
)

const (
	defaultShardingCount = 64
)

// NewShardingSharedInformer creates a new instance for the listwatcher.
func NewShardingSharedInformer(lw ListerWatcher, exampleObject runtime.Object, defaultEventHandlerResyncPeriod time.Duration) SharedInformer {
	return NewShardingSharedIndexInformer(lw, exampleObject, defaultEventHandlerResyncPeriod, Indexers{})
}

// NewShardingSharedIndexInformer creates a new instance for the listwatcher.
func NewShardingSharedIndexInformer(lw ListerWatcher, objType runtime.Object, defaultEventHandlerResyncPeriod time.Duration, indexers Indexers) SharedIndexInformer {
	return NewShardingSharedIndexInformerWithShardingCount(lw, objType, defaultEventHandlerResyncPeriod, Indexers{}, defaultShardingCount)
}

// NewShardingSharedIndexInformerWithShardingCount creates a new instance for the listwatcher.
func NewShardingSharedIndexInformerWithShardingCount(lw ListerWatcher, objType runtime.Object, defaultEventHandlerResyncPeriod time.Duration, indexers Indexers, shardingCount int) SharedIndexInformer {
	realClock := &clock.RealClock{}
	shardingSharedIndexInformer := &shardingSharedIndexInformer{
		sharedIndexInformers:            make(map[int]SharedIndexInformer),
		lw:                              lw,
		objType:                         objType,
		defaultEventHandlerResyncPeriod: defaultEventHandlerResyncPeriod,
		indexers:                        indexers,
		shardingCount:                   getShardingCount(shardingCount),
		shardingLabelKey:                getShardingLabelKey(),
		clock:                           realClock,
	}
	return shardingSharedIndexInformer
}

type shardingSharedIndexInformer struct {
	// key is sharding index, value is sharding shared index informer
	sharedIndexInformers map[int]SharedIndexInformer

	indexers Indexers
	lw       ListerWatcher

	// objectType is an example object of the type this informer is
	// expected to handle.  Only the type needs to be right, except
	// that when that is `unstructured.Unstructured` the object's
	// `"apiVersion"` and `"kind"` must also be right.
	objType runtime.Object

	// defaultEventHandlerResyncPeriod is the default resync period for any handlers added via
	// AddEventHandler (i.e. they don't specify one and just want to use the shared informer's default
	// value).
	defaultEventHandlerResyncPeriod time.Duration

	// clock allows for testability
	clock clock.Clock

	// current sharding count
	shardingCount    int
	shardingLabelKey string
	started          bool

	sync.RWMutex
	// handlers record event handlers
	handlers []ResourceEventHandler
	// handlersResyncPeriod record each resync period for event handlers
	handlersResyncPeriod []time.Duration
	addedIndexers        Indexers
}

func (s *shardingSharedIndexInformer) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()

	klog.V(2).Infof("Starting sharding informer for type %s", reflect.TypeOf(s.objType).String())
	s.initShardingInformers()
	for _, informer := range s.sharedIndexInformers {
		go informer.Run(stopCh)
	}

	s.Lock()
	s.started = true
	s.Unlock()
	<-stopCh
}

func (s *shardingSharedIndexInformer) initShardingInformers() {
	shardingCount := s.shardingCount
	for index := 0; index < shardingCount; index++ {
		shardingIndex := index
		s.Lock()
		if _, exists := s.sharedIndexInformers[shardingIndex]; !exists {
			lw := s.lw
			optionsModifier := func(options *metav1.ListOptions) {
				options.ShardingIndex = int64(shardingIndex)
				options.ShardingCount = int64(shardingCount)
				options.ShardingLabelKey = s.shardingLabelKey
				options.HashFunc = "FNV32"
			}

			listWatcher := &ListWatch{
				ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
					if optionsModifier != nil {
						optionsModifier(&options)
					}
					return lw.List(options)
				},
				WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
					options.Watch = true
					if optionsModifier != nil {
						optionsModifier(&options)
					}
					return lw.Watch(options)
				},
			}

			sharedIndexInformer := &sharedIndexInformer{
				processor:                       &sharedProcessor{clock: s.clock},
				indexer:                         NewIndexer(DeletionHandlingMetaNamespaceKeyFunc, s.indexers),
				listerWatcher:                   listWatcher,
				objectType:                      s.objType,
				resyncCheckPeriod:               s.defaultEventHandlerResyncPeriod,
				defaultEventHandlerResyncPeriod: s.defaultEventHandlerResyncPeriod,
				cacheMutationDetector:           NewCacheMutationDetector(fmt.Sprintf("%T", s.objType)),
				clock:                           s.clock,
			}
			s.sharedIndexInformers[shardingIndex] = sharedIndexInformer

			for i, handler := range s.handlers {
				s.sharedIndexInformers[shardingIndex].AddEventHandlerWithResyncPeriod(handler, s.handlersResyncPeriod[i])
			}
		}
		s.Unlock()
	}
}

func (s *shardingSharedIndexInformer) AddEventHandler(handler ResourceEventHandler) {
	s.AddEventHandlerWithResyncPeriod(handler, s.defaultEventHandlerResyncPeriod)
}

func (s *shardingSharedIndexInformer) AddEventHandlerWithResyncPeriod(handler ResourceEventHandler, resyncPeriod time.Duration) {
	s.Lock()
	s.handlers = append(s.handlers, handler)
	s.handlersResyncPeriod = append(s.handlersResyncPeriod, resyncPeriod)
	defer s.Unlock()
	for _, informer := range s.sharedIndexInformers {
		informer.AddEventHandlerWithResyncPeriod(handler, resyncPeriod)
	}
}

func (s *shardingSharedIndexInformer) GetStore() Store {
	return s
}

type shardedDummyController struct {
	*dummyController
	informer *shardingSharedIndexInformer
}

func (v *shardedDummyController) HasSynced() bool {
	return v.informer.HasSynced()
}

func (s *shardingSharedIndexInformer) GetController() Controller {
	return &shardedDummyController{
		dummyController: &dummyController{},
		informer:        s,
	}
}

func (s *shardingSharedIndexInformer) HasSynced() bool {
	s.RLock()
	defer s.RUnlock()
	if !s.started {
		return false
	}
	hasSynced := s.started
	for i := 0; i < s.shardingCount; i++ {
		if hasSynced = hasSynced && s.sharedIndexInformers[i].HasSynced(); !hasSynced {
			klog.V(7).Infof("sharding informer %v has not synced", i)
			return hasSynced
		}
	}
	return hasSynced
}

func (s *shardingSharedIndexInformer) LastSyncResourceVersion() string {
	return ""
}

func (s *shardingSharedIndexInformer) GetIndexer() Indexer {
	return s
}

func (s *shardingSharedIndexInformer) getSharedIndexInformerByObj(obj interface{}) (SharedIndexInformer, error) {
	key, err := DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		return nil, err
	}

	_, name, err := SplitMetaNamespaceKey(key)
	if err != nil {
		return nil, err
	}

	shardingIndex := sharding.HashFNV32(name) % int64(s.shardingCount)
	s.RLock()
	defer s.RUnlock()
	if informer, ok := s.sharedIndexInformers[int(shardingIndex)]; !ok {
		return nil, fmt.Errorf("sharding informer %v not found", shardingIndex)
	} else {
		return informer, nil
	}
}

// Store Interface
func (s *shardingSharedIndexInformer) Add(obj interface{}) error {
	informer, err := s.getSharedIndexInformerByObj(obj)
	if err != nil {
		return err
	}
	return informer.GetStore().Add(obj)
}

func (s *shardingSharedIndexInformer) Update(obj interface{}) error {
	informer, err := s.getSharedIndexInformerByObj(obj)
	if err != nil {
		return err
	}
	return informer.GetStore().Update(obj)
}

func (s *shardingSharedIndexInformer) Delete(obj interface{}) error {
	informer, err := s.getSharedIndexInformerByObj(obj)
	if err != nil {
		return err
	}
	return informer.GetStore().Delete(obj)
}

func (s *shardingSharedIndexInformer) List() []interface{} {
	s.RLock()
	defer s.RUnlock()
	var result []interface{}
	for i, informer := range s.sharedIndexInformers {
		list := informer.GetStore().List()
		klog.V(6).Infof("list %v objects from sharding informer %v", len(list), i)
		result = append(result, list...)
	}
	return result
}

func (s *shardingSharedIndexInformer) ListKeys() []string {
	s.RLock()
	defer s.RUnlock()
	var result []string
	for i, informer := range s.sharedIndexInformers {
		list := informer.GetStore().ListKeys()
		klog.V(4).Infof("sharding informer %v list %v keys", i, len(list))
		result = append(result, list...)
	}
	return result
}

func (s *shardingSharedIndexInformer) Get(obj interface{}) (item interface{}, exists bool, err error) {
	s.RLock()
	defer s.RUnlock()

	errGroup := new(errgroup.Group)
	for _, i := range s.sharedIndexInformers {
		informer := i
		errGroup.Go(func() error {
			object, exists, err := informer.GetStore().Get(obj)
			if exists {
				item = object
			}
			return err
		})
	}
	if err = errGroup.Wait(); err != nil {
		return nil, false, err
	}
	if item != nil {
		return item, true, nil
	}
	return nil, false, nil
}

func (s *shardingSharedIndexInformer) GetByKey(key string) (item interface{}, exists bool, err error) {
	s.RLock()
	defer s.RUnlock()
	errGroup := new(errgroup.Group)
	for _, i := range s.sharedIndexInformers {
		informer := i
		errGroup.Go(func() error {
			object, exists, err := informer.GetStore().GetByKey(key)
			if exists {
				item = object
			}
			return err
		})
	}
	if err = errGroup.Wait(); err != nil {
		return nil, false, err
	}
	if item != nil {
		return item, true, nil
	}
	return nil, false, nil
}

func (s *shardingSharedIndexInformer) Len() int {
	s.RLock()
	defer s.RUnlock()
	var length int
	for _, informer := range s.sharedIndexInformers {
		length += informer.GetStore().Len()
	}
	return length
}

// Replace will delete the contents of the store, using instead the
// given list. Store takes ownership of the list, you should not reference
// it after calling this function.
func (s *shardingSharedIndexInformer) Replace(list []interface{}, resourceVersion string) error {
	return fmt.Errorf("not supported")
}

func (s *shardingSharedIndexInformer) Resync() error {
	s.RLock()
	defer s.RUnlock()
	for _, informer := range s.sharedIndexInformers {
		informer.GetStore().Resync()
	}
	return nil
}

// Indexer interface
// Retrieve list of objects that match on the named indexing function
func (s *shardingSharedIndexInformer) Index(indexName string, obj interface{}) ([]interface{}, error) {
	s.RLock()
	defer s.RUnlock()
	var result []interface{}
	for _, informer := range s.sharedIndexInformers {
		informerResult, err := informer.GetIndexer().Index(indexName, obj)
		if err != nil {
			return nil, err
		} else {
			result = append(result, informerResult...)
		}
	}
	return result, nil
}

// IndexKeys returns the set of keys that match on the named indexing function.
func (s *shardingSharedIndexInformer) IndexKeys(indexName, indexKey string) ([]string, error) {
	s.RLock()
	defer s.RUnlock()
	var result []string
	for _, informer := range s.sharedIndexInformers {
		informerResult, err := informer.GetIndexer().IndexKeys(indexName, indexKey)
		if err != nil {
			return nil, err
		} else {
			result = append(result, informerResult...)
		}
	}
	return result, nil
}

// ListIndexFuncValues returns the list of generated values of an Index func
func (s *shardingSharedIndexInformer) ListIndexFuncValues(indexName string) []string {
	s.RLock()
	defer s.RUnlock()
	var result []string
	for _, informer := range s.sharedIndexInformers {
		informerResult := informer.GetIndexer().ListIndexFuncValues(indexName)
		result = append(result, informerResult...)
	}
	return result
}

// ByIndex lists object that match on the named indexing function with the exact key
func (s *shardingSharedIndexInformer) ByIndex(indexName, indexKey string) ([]interface{}, error) {
	s.RLock()
	defer s.RUnlock()
	var result []interface{}
	for _, informer := range s.sharedIndexInformers {
		informerResult, err := informer.GetIndexer().ByIndex(indexName, indexKey)
		if err != nil {
			return nil, err
		} else {
			result = append(result, informerResult...)
		}
	}
	return result, nil
}

// GetIndexer return the indexers
func (s *shardingSharedIndexInformer) GetIndexers() Indexers {
	return s.indexers
}

func (s *shardingSharedIndexInformer) AddIndexers(indexers Indexers) error {
	s.RLock()
	defer s.RUnlock()
	for _, informer := range s.sharedIndexInformers {
		if err := informer.AddIndexers(indexers); err != nil {
			return err
		}
	}
	for key, indexer := range indexers {
		s.indexers[key] = indexer
	}
	return nil
}

func getShardingCount(userShardingCount int) int {
	if userShardingCount > 0 {
		return userShardingCount
	}
	shardingCount := defaultShardingCount
	return shardingCount
}

func getShardingLabelKey() string {
	return sharding.DefaultInformerShardingLabelKey
}
