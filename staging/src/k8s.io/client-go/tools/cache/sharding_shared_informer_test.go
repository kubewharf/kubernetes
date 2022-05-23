/*
Copyright 2017 The Kubernetes Authors.

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
	"testing"
	"time"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/apimachinery/pkg/util/sets"
	fcache "k8s.io/client-go/tools/cache/testing"
)

func TestShardingInformerListenerResyncPeriods(t *testing.T) {
	// source simulates an apiserver object endpoint.
	source := fcache.NewFakeControllerSource()
	source.Add(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod1"}})
	source.Add(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod2"}})

	// create the shared informer and resync every 1s
	informer := NewShardingSharedInformer(source, &v1.Pod{}, 1*time.Second).(*shardingSharedIndexInformer)

	clock := clock.NewFakeClock(time.Now())
	informer.clock = clock

	// listener 1, never resync
	listener1 := newTestListener("listener1", 0, "pod1", "pod2")
	informer.AddEventHandlerWithResyncPeriod(listener1, listener1.resyncPeriod)

	// listener 2, resync every 2s
	listener2 := newTestListener("listener2", 2*time.Second, "pod1", "pod2")
	informer.AddEventHandlerWithResyncPeriod(listener2, listener2.resyncPeriod)

	// listener 3, resync every 3s
	listener3 := newTestListener("listener3", 3*time.Second, "pod1", "pod2")
	informer.AddEventHandlerWithResyncPeriod(listener3, listener3.resyncPeriod)
	listeners := []*testListener{listener1, listener2, listener3}

	stop := make(chan struct{})
	defer close(stop)

	go informer.Run(stop)

	// ensure all listeners got the initial List
	for _, listener := range listeners {
		if !listener.ok() {
			t.Errorf("%s: expected %v, got %v", listener.name, listener.expectedItemNames, listener.receivedItemNames)
		}
	}

	// reset
	for _, listener := range listeners {
		listener.receivedItemNames = []string{}
	}

	// advance so listener2 gets a resync
	clock.Step(2 * time.Second)

	// make sure listener2 got the resync
	if !listener2.ok() {
		t.Errorf("%s: expected %v, got %v", listener2.name, listener2.expectedItemNames, listener2.receivedItemNames)
	}

	// wait a bit to give errant items a chance to go to 1 and 3
	time.Sleep(1 * time.Second)

	// make sure listeners 1 and 3 got nothing
	if len(listener1.receivedItemNames) != 0 {
		t.Errorf("listener1: should not have resynced (got %d)", len(listener1.receivedItemNames))
	}
	if len(listener3.receivedItemNames) != 0 {
		t.Errorf("listener3: should not have resynced (got %d)", len(listener3.receivedItemNames))
	}

	// reset
	for _, listener := range listeners {
		listener.receivedItemNames = []string{}
	}

	// advance so listener3 gets a resync
	clock.Step(1 * time.Second)

	// make sure listener3 got the resync
	if !listener3.ok() {
		t.Errorf("%s: expected %v, got %v", listener3.name, listener3.expectedItemNames, listener3.receivedItemNames)
	}

	// wait a bit to give errant items a chance to go to 1 and 2
	time.Sleep(1 * time.Second)

	// make sure listeners 1 and 2 got nothing
	if len(listener1.receivedItemNames) != 0 {
		t.Errorf("listener1: should not have resynced (got %d)", len(listener1.receivedItemNames))
	}
	if len(listener2.receivedItemNames) != 0 {
		t.Errorf("listener2: should not have resynced (got %d)", len(listener2.receivedItemNames))
	}
}

func TestShardingInformerResyncCheckPeriod(t *testing.T) {
	// source simulates an apiserver object endpoint.
	source := fcache.NewFakeControllerSource()

	// create the shared informer and resync every 12 hours
	informer := NewShardingSharedInformer(source, &v1.Pod{}, 12*time.Hour).(*shardingSharedIndexInformer)

	clock := clock.NewFakeClock(time.Now())
	informer.clock = clock
	informer.initShardingInformers()

	// listener 1, never resync
	listener1 := newTestListener("listener1", 0)
	informer.AddEventHandlerWithResyncPeriod(listener1, listener1.resyncPeriod)

	for _, shardingInformer := range informer.sharedIndexInformers {
		shardingSharedInformer := shardingInformer.(*sharedIndexInformer)
		if e, a := 12*time.Hour, shardingSharedInformer.resyncCheckPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
		if e, a := time.Duration(0), shardingSharedInformer.processor.listeners[0].resyncPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
	}

	// listener 2, resync every minute
	listener2 := newTestListener("listener2", 1*time.Minute)
	informer.AddEventHandlerWithResyncPeriod(listener2, listener2.resyncPeriod)
	for _, shardingInformer := range informer.sharedIndexInformers {
		shardingSharedInformer := shardingInformer.(*sharedIndexInformer)
		if e, a := 1*time.Minute, shardingSharedInformer.resyncCheckPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
		if e, a := time.Duration(0), shardingSharedInformer.processor.listeners[0].resyncPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
		if e, a := 1*time.Minute, shardingSharedInformer.processor.listeners[1].resyncPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
	}

	// listener 3, resync every 55 seconds
	listener3 := newTestListener("listener3", 55*time.Second)
	informer.AddEventHandlerWithResyncPeriod(listener3, listener3.resyncPeriod)
	for _, shardingInformer := range informer.sharedIndexInformers {
		shardingSharedInformer := shardingInformer.(*sharedIndexInformer)
		if e, a := 55*time.Second, shardingSharedInformer.resyncCheckPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
		if e, a := time.Duration(0), shardingSharedInformer.processor.listeners[0].resyncPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
		if e, a := 1*time.Minute, shardingSharedInformer.processor.listeners[1].resyncPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
		if e, a := 55*time.Second, shardingSharedInformer.processor.listeners[2].resyncPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
	}

	// listener 4, resync every 5 seconds
	listener4 := newTestListener("listener4", 5*time.Second)
	informer.AddEventHandlerWithResyncPeriod(listener4, listener4.resyncPeriod)
	for _, shardingInformer := range informer.sharedIndexInformers {
		shardingSharedInformer := shardingInformer.(*sharedIndexInformer)
		if e, a := 5*time.Second, shardingSharedInformer.resyncCheckPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
		if e, a := time.Duration(0), shardingSharedInformer.processor.listeners[0].resyncPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
		if e, a := 1*time.Minute, shardingSharedInformer.processor.listeners[1].resyncPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
		if e, a := 55*time.Second, shardingSharedInformer.processor.listeners[2].resyncPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
		if e, a := 5*time.Second, shardingSharedInformer.processor.listeners[3].resyncPeriod; e != a {
			t.Errorf("expected %d, got %d", e, a)
		}
	}
}

// verify that https://github.com/kubernetes/kubernetes/issues/59822 is fixed
func TestShardingSharedInformerInitializationRace(t *testing.T) {
	source := fcache.NewFakeControllerSource()
	informer := NewShardingSharedInformer(source, &v1.Pod{}, 1*time.Second).(*shardingSharedIndexInformer)
	listener := newTestListener("raceListener", 0)

	stop := make(chan struct{})
	go informer.AddEventHandlerWithResyncPeriod(listener, listener.resyncPeriod)
	go informer.Run(stop)
	close(stop)
}

// TestShardingSharedInformerWatchDisruption simulates a watch that was closed
// with updates to the store during that time. We ensure that handlers with
// resync and no resync see the expected state.
func TestShardingSharedInformerWatchDisruption(t *testing.T) {
	// source simulates an apiserver object endpoint.
	source := fcache.NewFakeControllerSource()

	source.Add(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod1", UID: "pod1", ResourceVersion: "1"}})
	source.Add(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod2", UID: "pod2", ResourceVersion: "2"}})

	// create the shared informer and resync every 1s
	informer := NewShardingSharedInformer(source, &v1.Pod{}, 1*time.Second).(*shardingSharedIndexInformer)

	clock := clock.NewFakeClock(time.Now())
	informer.clock = clock
	//informer.processor.clock = clock

	// listener, never resync
	listenerNoResync := newTestListener("listenerNoResync", 0, "pod1", "pod2")
	informer.AddEventHandlerWithResyncPeriod(listenerNoResync, listenerNoResync.resyncPeriod)

	listenerResync := newTestListener("listenerResync", 1*time.Second, "pod1", "pod2")
	informer.AddEventHandlerWithResyncPeriod(listenerResync, listenerResync.resyncPeriod)
	listeners := []*testListener{listenerNoResync, listenerResync}

	stop := make(chan struct{})
	defer close(stop)

	go informer.Run(stop)

	for _, listener := range listeners {
		if !listener.ok() {
			t.Errorf("%s: expected %v, got %v", listener.name, listener.expectedItemNames, listener.receivedItemNames)
		}
	}

	// Add pod3, bump pod2 but don't broadcast it, so that the change will be seen only on relist
	source.AddDropWatch(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod3", UID: "pod3", ResourceVersion: "3"}})
	source.ModifyDropWatch(&v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod2", UID: "pod2", ResourceVersion: "4"}})

	// Ensure that nobody saw any changes
	for _, listener := range listeners {
		if !listener.ok() {
			t.Errorf("%s: expected %v, got %v", listener.name, listener.expectedItemNames, listener.receivedItemNames)
		}
	}

	for _, listener := range listeners {
		listener.receivedItemNames = []string{}
	}

	listenerNoResync.expectedItemNames = sets.NewString("pod2", "pod3")
	listenerResync.expectedItemNames = sets.NewString("pod1", "pod2", "pod3")

	// This calls shouldSync, which deletes noResync from the list of syncingListeners
	clock.Step(1 * time.Second)

	// Simulate a connection loss (or even just a too-old-watch)
	source.ResetWatch()

	for _, listener := range listeners {
		if !listener.ok() {
			t.Errorf("%s: expected %v, got %v", listener.name, listener.expectedItemNames, listener.receivedItemNames)
		}
	}
}
