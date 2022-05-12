package client

import (
	"context"

	"k8s.io/apiserver/pkg/storage/storagebackend/db/api"
)

type Client interface {
	// Get return value and revision found at key. On a not found err, will not return error.
	// if key's value is empty, return without error.
	Get(ctx context.Context, key string) (dbRevision, kvRevision int64, value []byte, err error)

	// Range get kv pairs found between key and end at the snapshot of revision.
	// if revision = 0, return the result in newest read view. if revision > 0, result will be in the snapshot of specified revision.
	// if revision is too old and has been compacted, will return err.
	// if limit > 0, kvs' length is guaranteed to be no bigger than limit. if kvs' number in database is bigger than limit, more is true
	// rangeRevision is the revision of database at the spot of serving range request
	Range(ctx context.Context, key, end string, limit, revision int64) (rangeRevision int64, kvs []*api.KeyValue, more bool, totalCount int64, err error)

	// Create adds a kv pair unless it already exists. 'ttl' is time-to-live in seconds (0 means forever).
	// If no error is returned, success is true, revision is key's revision in database.
	// Otherwise, success is false, revision is 0
	Create(ctx context.Context, key string, value []byte, ttl uint64) (success bool, revision int64, err error)

	// Delete removes the specified key from database. deletion will be handled with revision cas.
	// If key didn't exist, success will be false, newestRevision is 0, value is nil, err is nil.
	// if cas is failed, success will be false, newestRevision and value will be the revision and value from database at that spot
	// if cas is successful, success will be true, newestRevision = revision, value returns nil to improve performance
	Delete(ctx context.Context, key string, revision int64) (success bool, newestRevision int64, value []byte, err error)

	// Update updates the specified key with value and ttl in database. update will be handled with revision cas.
	// is key didn't exits, success will be false, newestRevision is 0, newestValue is nil, err is nil
	// if cas is failed, success will be false, newestRevision and newestValue will be the revision and value from database at that spot
	// if cas is successful, success will be true, newestRevision = new revision of key, newestValue = value
	Update(ctx context.Context, key string, revision int64, value []byte, ttl uint64) (success bool, newestRevision int64, newestValue []byte, err error)

	// Count returns the number of kv pairs lies between start and end in database
	Count(ctx context.Context, start, end string) (int64, error)

	// Watch begins watching the specified key's value. If recursive is true, all keys with key prefix will be watched.
	// Response items are decoded into backend.WatchResponse
	// resourceVersion may be used to specify what version to begin watching, If resource version is "0", will watch from now on
	Watch(ctx context.Context, key string, revision int64, recursive bool) chan api.WatchResponse

	// Name returns the name of client implementation
	Name() string

	// Close do gc work in Client
	Close() error

	// StartCompact starts a compactor in the background to compact old version of keys that's not needed.
	Compact(ctx context.Context, revision int64) error

	// DisableLimit decides whether range request should disable limit, if true, paging is not valid
	DisableLimit() bool
}
