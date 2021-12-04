/*
Copyright 2016 The Kubernetes Authors.

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

package api

import (
	"fmt"
)

type WatchResponse struct {
	Err    error
	Events []*Event
}

type KeyValue struct {
	Key   string
	Value []byte
	Rev   int64
}

type Event struct {
	Key       string
	Value     []byte
	PrevValue []byte
	Rev       int64
	IsDeleted bool
	IsCreated bool
}

// ParseKV converts a KeyValue retrieved from an initial sync() listing to a synthetic isCreated event.
func ParseKV(kv *KeyValue) *Event {
	return &Event{
		Key:       string(kv.Key),
		Value:     kv.Value,
		PrevValue: nil,
		Rev:       kv.Rev,
		IsDeleted: false,
		IsCreated: true,
	}
}

func ValidateEvent(e *Event, ignoreModPrev bool) error {
	if !ignoreModPrev {
		if !e.IsCreated && e.PrevValue == nil {
			// If the previous value is nil, error. One example of how this is possible is if the previous value has been compacted already.
			return fmt.Errorf("etcd event received with PrevKv=nil (key=%q, modRevision=%d, created=%v, deleted=%v)", string(e.Key), e.Rev, e.IsCreated, e.IsDeleted)
		}
	}
	if e.IsDeleted == true && e.PrevValue == nil {
		return fmt.Errorf("etcd delete event received with PrevKv=nil (key=%q, modRevision=%d, created=%v, deleted=%v)", string(e.Key), e.Rev, e.IsCreated, e.IsDeleted)
	}
	return nil
}
