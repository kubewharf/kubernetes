package kubetracing

import (
	"sync"

	"github.com/opentracing/opentracing-go"
)

const (
	KubetracingSpanKey         = "KubetracingSpanKey"
	KubetracingSpanContextKey  = "KubetracingSpanContextKey"
	KubetracingSpanFinishedTag = "finished"
)

// TODO: Add reference counter and garbage collection.
type spanStore struct {
	lock  sync.RWMutex
	store map[string]opentracing.Span
}

var (
	globalSpanStore = spanStore{store: make(map[string]opentracing.Span)}
)

func (s *spanStore) get(key string) opentracing.Span {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.store[key]
}

func (s *spanStore) set(key string, span opentracing.Span) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.store[key] = span
}

func (s *spanStore) del(key string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.store, key)
}
