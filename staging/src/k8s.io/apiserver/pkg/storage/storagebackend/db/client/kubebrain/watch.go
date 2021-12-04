package kubebrain

import (
	"context"

	proto "code.byted.org/tce/kubebrain-client/api/v2rpc"
	"code.byted.org/tce/kubebrain-client/client"

	"k8s.io/apiserver/pkg/storage/storagebackend/db/api"
	"k8s.io/apiserver/pkg/storage/storagebackend/db/common"
)

// Watch implements client.Interface.Watch.
func (b *brainClient) Watch(ctx context.Context, key string, revision int64, recursive bool) chan api.WatchResponse {
	request := &proto.WatchRequest{
		Key:      []byte(key),
		Revision: uint64(revision),
	}
	if recursive {
		request.End = []byte(common.GetPrefixRangeEnd(key))
	}
	watchChan := make(chan api.WatchResponse, 1000)
	go b.watch(ctx, key, revision, watchChan)
	return watchChan
}

func (b *brainClient) watch(ctx context.Context, prefix string, rev int64, watchChan chan api.WatchResponse) {
	defer close(watchChan)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	wch := b.client.Watch(ctx, prefix, client.WithPrefix(), client.WithRevision(uint64(rev)))
	for wresp := range wch {
		if err := wresp.Err(); err != nil {
			watchChan <- api.WatchResponse{
				Err: err,
			}
			return
		}

		events := make([]*api.Event, len(wresp.Events))
		for idx, event := range wresp.Events {
			events[idx] = parseEvent(event)
		}
		watchChan <- api.WatchResponse{
			Events: events,
		}
	}
}

// transform from brain event to backend event
func parseEvent(e *proto.Event) *api.Event {
	event := &api.Event{
		Key:       string(e.Kv.Key),
		Rev:       int64(e.Revision),
		IsDeleted: e.Type == proto.Event_DELETE,
		IsCreated: e.Type == proto.Event_CREATE,
	}
	if event.IsDeleted {
		event.PrevValue = e.Kv.Value
	} else {
		event.Value = e.Kv.Value
	}
	return event
}
