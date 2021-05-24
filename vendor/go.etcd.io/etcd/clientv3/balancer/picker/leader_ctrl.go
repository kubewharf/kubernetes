package picker

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/resolver"
)

var globalLeaderCtrl = leaderCtrl{
	leaderIndex: make(map[string]*leaderRecord),
}

type leaderCtrl struct {
	// leaderIndex restore the leaders of clusters
	leaderIndex map[string]*leaderRecord
	sync.RWMutex
	Group
}

func (l *leaderCtrl) needUpdateLeader(key string, leader string) bool {
	l.RLock()
	defer l.RUnlock()
	record, ok := l.leaderIndex[key]
	if !ok {
		return false
	}
	return record.getLeader() == leader
}

// updateClusterLeader is safe for calling concurrently to update cluter info
func (l *leaderCtrl) updateClusterLeader(clusterKey string, leader string) {
	if !l.needUpdateLeader(clusterKey, leader) {
		return
	}
	l.Lock()
	defer l.Unlock()
	record, ok := l.leaderIndex[clusterKey]
	if !ok {
		return
	}
	record.setLeader(leader)
}

func (l *leaderCtrl) updateClusterAmount(eps []string, amount int, cb func() (resolver.Address, error)) {
	if len(eps) < 0 {
		return
	}

	clusterKey := marshalEndpoints(eps)

	l.Lock()
	defer l.Unlock()

	record, ok := l.leaderIndex[clusterKey]
	if !ok && amount > 0 {
		record = newLeaderRecord(cb)
		l.leaderIndex[clusterKey] = record
		if len(eps) == 1 {
			return
		}

		for _, ep := range eps {
			if _, ok := l.leaderIndex[ep]; ok {
				record.cancel()
				panic("do not support one endpoint in different cluster")
			}
			l.leaderIndex[ep] = record
		}

		return
	} else if !ok && amount <= 0 {
		return
	}

	record.amount += amount
	if record.amount <= 0 {
		delete(l.leaderIndex, clusterKey)
		for _, ep := range eps {
			delete(l.leaderIndex, ep)
		}
		record.cancel()
		l.Forget(clusterKey)
	}
}

func (l *leaderCtrl) getLeaderAddr(key string) string {
	l.RLock()
	defer l.RUnlock()

	record, ok := l.leaderIndex[key]
	if !ok {
		return ""
	}
	return record.getLeader()
}

type leaderRecord struct {
	amount int
	leader string
	ctx    context.Context
	mu     sync.RWMutex
	cancel func()
	cb     func() (resolver.Address, error)
}

func newLeaderRecord(cb func() (resolver.Address, error)) *leaderRecord {
	ctx, cancel := context.WithCancel(context.Background())
	l := &leaderRecord{
		amount: 1,
		ctx:    ctx,
		mu:     sync.RWMutex{},
		cancel: cancel,
		cb:     cb,
	}
	addr, _ := cb()
	l.leader = addr.Addr
	go l.healthCheck()
	return l
}

func (l *leaderRecord) setLeader(leader string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.leader = leader
}

func (l *leaderRecord) getLeader() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.leader
}

func (l *leaderRecord) healthCheck() {
	ticker := time.NewTicker(time.Second)
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
			addr, err := l.cb()
			if err != nil {
				continue
			}
			l.setLeader(addr.Addr)
		}
	}
}

func marshalEndpoints(endpoints []string) string {
	sortedEndpoints := make([]string, len(endpoints))
	copy(sortedEndpoints, endpoints)
	sort.Strings(sortedEndpoints)
	return strings.Join(sortedEndpoints, ",")
}

func RegisterCluster(endpoint []string, cb func() (resolver.Address, error)) {
	globalLeaderCtrl.updateClusterAmount(endpoint, 1, cb)
	updateClusterLeader(marshalEndpoints(endpoint), cb)
}

func UpdateClusterLeader(endpoints []string, cb func() (resolver.Address, error)) {
	key := marshalEndpoints(endpoints)
	updateClusterLeader(key, cb)
}

func updateClusterLeader(clusterKey string, cb func() (resolver.Address, error)) {
	if cb == nil {
		return
	}
	// use single flight to avoid duplicate
	ctrl := globalLeaderCtrl
	_, _, _ = ctrl.Do(clusterKey, func() (interface{}, error) {
		leader, err := cb()
		if err == nil {
			globalLeaderCtrl.updateClusterLeader(clusterKey, leader.Addr)
		}
		return leader, err
	})
}

func UnregisterCluster(endpoints []string) {
	if len(endpoints) < 0 {
		return
	}
	globalLeaderCtrl.updateClusterAmount(endpoints, -1, nil)
}

func GetLeader(endpoints []string) string {
	if len(endpoints) < 0 {
		return ""
	}
	return globalLeaderCtrl.getLeaderAddr(endpoints[0])
}
