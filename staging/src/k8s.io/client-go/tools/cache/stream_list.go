/*
Copyright 2022 The Kubernetes Authors.

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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/naming"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/klog"
)

// StreamListerWatcher is any object that knows how to perform an
// initial list from watch and start a watch on a resource.
type StreamListerWatcher interface {
	ListerWatcher

	// StreamList should return a list of objects and current resourceVersion
	StreamList(options metav1.ListOptions) ([]runtime.Object, string, error)
}

type StreamListWatch struct {
	name string
	lw   ListerWatcher

	// The name of the type we expect to place in the store. The name
	// will be the stringification of expectedGVK if provided, and the
	// stringification of expectedType otherwise. It is for display
	// only, and should not be used for parsing or comparison.
	expectedTypeName string
	// An example object of the type we expect to place in the store.
	// Only the type needs to be right, except that when that is
	// `unstructured.Unstructured` the object's `"apiVersion"` and
	// `"kind"` must also be right.
	expectedType reflect.Type
	// The GVK of the object we expect to place in the store if unstructured.
	expectedGVK *schema.GroupVersionKind
}

func NewStreamListerWatcher(lw ListerWatcher, expectedType interface{}) StreamListerWatcher {
	return NewNamedStreamListerWatcher(naming.GetNameFromCallsite(internalPackages...), lw, expectedType)
}

func NewNamedStreamListerWatcher(name string, lw ListerWatcher, expectedType interface{}) StreamListerWatcher {
	l := &StreamListWatch{
		name: name,
		lw:   lw,
	}
	l.setExpectedType(expectedType)
	return l
}

func NewStreamListerWatcherFor(lw ListerWatcher, expectedTypeName string, expectedType reflect.Type, expectedGVK *schema.GroupVersionKind) StreamListerWatcher {
	l := &StreamListWatch{
		name:             naming.GetNameFromCallsite(internalPackages...),
		lw:               lw,
		expectedTypeName: expectedTypeName,
		expectedType:     expectedType,
		expectedGVK:      expectedGVK,
	}
	return l
}

// Watch should begin a watch at the specified version.
func (l *StreamListWatch) Watch(options metav1.ListOptions) (watch.Interface, error) {
	return l.lw.Watch(options)
}

func (l *StreamListWatch) List(options metav1.ListOptions) (runtime.Object, error) {
	items, resourceVersion, err := l.StreamList(options)
	if err != nil {
		return nil, err
	}
	return &metainternalversion.List{ListMeta: metav1.ListMeta{ResourceVersion: resourceVersion}, Items: items}, nil
}

func (l *StreamListWatch) StreamList(options metav1.ListOptions) ([]runtime.Object, string, error) {
	options.Continue = ""
	options.Limit = 0
	options.AllowWatchBookmarks = true
	options.ResourceVersion = "0"
	options.Watch = true
	options.ListFromWatch = true

	w, err := l.lw.Watch(options)
	if err != nil {
		klog.Errorf("failed to create watch for %v: %v", l.expectedTypeName, err)
		return nil, "", err
	}
	defer w.Stop()

	temporaryStore := NewIndexer(DeletionHandlingMetaNamespaceKeyFunc, Indexers{})
	resourceVersion, err := l.watchHandler(temporaryStore, w)
	if err != nil {
		return nil, "", err
	}
	// get all items from watcher
	items := temporaryStore.List()
	listItems := make([]runtime.Object, len(items))
	for i, o := range items {
		listItems[i] = o.(runtime.Object)
	}
	return listItems, resourceVersion, nil
}

func (l *StreamListWatch) watchHandler(store Store, w watch.Interface) (string, error) {
	var resourceVersion string

loop:
	for {
		event, ok := <-w.ResultChan()
		if !ok {
			return "", fmt.Errorf("watch channel closed unexpected")
		}
		if event.Type == watch.Error {
			return "", apierrors.FromObject(event.Object)
		}
		if l.expectedType != nil {
			if e, a := l.expectedType, reflect.TypeOf(event.Object); e != a {
				utilruntime.HandleError(fmt.Errorf("%s: expected type %v, but watch event object had type %v", l.name, e, a))
				continue
			}
		}
		if l.expectedGVK != nil {
			if e, a := *l.expectedGVK, event.Object.GetObjectKind().GroupVersionKind(); e != a {
				utilruntime.HandleError(fmt.Errorf("%s: expected gvk %v, but watch event object had gvk %v", l.name, e, a))
				continue
			}
		}
		meta, err := meta.Accessor(event.Object)
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("%s: unable to understand watch event %#v", l.name, event))
			continue
		}
		switch event.Type {
		case watch.Added:
			if err := store.Add(event.Object); err != nil {
				return "", err
			}
		case watch.Bookmark:
			resourceVersion = meta.GetResourceVersion()
			break loop
		case watch.Modified, watch.Deleted:
			utilruntime.HandleError(fmt.Errorf("%s: unexpected watch event type in stream list %v", l.name, event.Type))
		default:
			utilruntime.HandleError(fmt.Errorf("%s: unable to understand watch event %#v", l.name, event))
		}
	}

	return resourceVersion, nil
}

func (l *StreamListWatch) setExpectedType(expectedType interface{}) {
	l.expectedType = reflect.TypeOf(expectedType)
	if l.expectedType == nil {
		l.expectedTypeName = defaultExpectedTypeName
		return
	}

	l.expectedTypeName = l.expectedType.String()

	if obj, ok := expectedType.(*unstructured.Unstructured); ok {
		// Use gvk to check that watch event objects are of the desired type.
		gvk := obj.GroupVersionKind()
		if gvk.Empty() {
			klog.V(4).Infof("StreamingLister from %s configured with expectedType of *unstructured.Unstructured with empty GroupVersionKind.", l.name)
			return
		}
		l.expectedGVK = &gvk
		l.expectedTypeName = gvk.String()
	}
}
