package picker

import (
	"context"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/resolver"
)

func newRWSeparatedRoundRobinBalanced(cfg Config) Picker {
	addrToIdx := make(map[string]int)
	scs := make([]balancer.SubConn, 0, len(cfg.SubConnToResolverAddress))
	key := ""
	for sc, addr := range cfg.SubConnToResolverAddress {
		addrToIdx[addr.Addr] = len(scs)
		scs = append(scs, sc)
		key = addr.Addr // set key to the last value (random one)
	}
	return &rwSeparatedRoundBalanced{
		p:         RWSeparatedRoundrobinBalanced,
		lg:        cfg.Logger,
		scs:       scs,
		scToAddr:  cfg.SubConnToResolverAddress,
		key:       key,
		addrToIdx: addrToIdx,
		readerScs: make([]balancer.SubConn, 0, len(cfg.SubConnToResolverAddress)),
	}
}

type rwSeparatedRoundBalanced struct {
	p Policy

	lg *zap.Logger

	key             string
	mu              sync.RWMutex
	scs             []balancer.SubConn
	scToAddr        map[balancer.SubConn]resolver.Address
	addrToIdx       map[string]int
	nextHealthCheck int64
	nextReader      int64
	leader          string             // protected by mu
	readerScs       []balancer.SubConn // protected by mu
}

func (rw *rwSeparatedRoundBalanced) String() string { return rw.p.String() }

// special FullMethodName
const (
	etcdServerRead  = "/etcdserverpb.KV/Range"
	etcdServerWatch = "/etcdserverpb.Watch/Watch"
	etcdServerTxn   = "/etcdserverpb.KV/Txn"
	grpcHealthCheck = "/grpc.health.v1.Health/Check"
)

// Pick is called for every client request.
func (rw *rwSeparatedRoundBalanced) Pick(ctx context.Context, opts balancer.PickOptions) (sc balancer.SubConn, f func(balancer.DoneInfo), err error) {

	n := len(rw.scs)
	if n == 0 {
		rw.lg.Warn("no SubConn avaiable", zap.Strings("endpoints", map2slices(rw.addrToIdx)))
		return nil, nil, balancer.ErrNoSubConnAvailable
	}

	//! there may be a difference between the pick result and real expected.
	//! the difference can not be avoid by lock, but it can work well.
	//! - for read method:
	//! 	current resp should be right which may be returned by leader,
	//!		and other reqs will be assigned to reader soon
	//! - for write method:
	//!		current resp should be wrong while req was assigned to reader or conn has been closed,
	//!		and a healthCheck will be triggered to correct the assignment soon.
	p := rw.selectPickFunc(opts)
	cur, sc, picked, err := p()
	if err != nil {
		rw.lg.Warn("no SubConn avaiable", zap.Strings("endpoints", map2slices(rw.addrToIdx)))
		return nil, nil, balancer.ErrNoSubConnAvailable
	}

	rw.lg.Debug(
		"picked",
		zap.String("picker", rw.p.String()),
		zap.String("address", picked),
		zap.Int("subconn-index", cur),
		zap.Int("subconn-size", n),
	)

	doneFunc := func(info balancer.DoneInfo) {
		// TODO: error handling?
		fss := []zapcore.Field{
			zap.Error(info.Err),
			zap.String("picker", rw.p.String()),
			zap.String("address", picked),
			zap.Bool("success", info.Err == nil),
			zap.Bool("bytes-sent", info.BytesSent),
			zap.Bool("bytes-received", info.BytesReceived),
		}
		if info.Err == nil {
			rw.lg.Debug("balancer done", fss...)
		} else {
			rw.lg.Warn("balancer failed", fss...)
		}
	}
	return sc, doneFunc, nil
}

type pickerFunc func() (int, balancer.SubConn, string, error)

func (rw *rwSeparatedRoundBalanced) pickReader() (cur int, sc balancer.SubConn, picked string, err error) {

	globalLeaderCtrl.RLock()
	defer globalLeaderCtrl.RUnlock()

	_ = rw.updateLeader()

	rw.mu.RLock()
	defer rw.mu.RUnlock()
	l := len(rw.readerScs)
	if l < 1 {
		return rw.pickWriter()
	}

	cur = int((atomic.AddInt64(&rw.nextReader, 1) - 1)) % l
	sc = rw.readerScs[cur]
	picked = rw.scToAddr[sc].Addr
	return cur, sc, picked, nil
}

func (rw *rwSeparatedRoundBalanced) pickWriter() (int, balancer.SubConn, string, error) {
	// pick the leader
	globalLeaderCtrl.RLock()
	defer globalLeaderCtrl.RUnlock()

	picked := rw.updateLeader()
	// if there is no leader, just return no availble and wait for updating
	cur, exist := rw.addrToIdx[picked]
	if !exist {
		rw.lg.Error("no sub conn", zap.String("picked", picked), zap.Strings("addrs", map2slices(rw.addrToIdx)))
		return 0, nil, "", balancer.ErrNoSubConnAvailable
	}

	sc := rw.scs[cur]
	return cur, sc, picked, nil
}

func (rw *rwSeparatedRoundBalanced) pickHealthChecker() (int, balancer.SubConn, string, error) {
	// round robin over all endpoints
	cur := int((atomic.AddInt64(&rw.nextHealthCheck, 1) - 1)) % len(rw.scs)
	sc := rw.scs[cur]
	picked := rw.scToAddr[sc].Addr
	return cur, sc, picked, nil
}

func map2slices(m map[string]int) []string {
	var ss []string
	for k := range m {
		ss = append(ss, k)
	}
	return ss
}

func (rw *rwSeparatedRoundBalanced) updateLeader() string {

	// fast compare without write lock
	if equal, curLeader := rw.compareLeader(); equal {
		return curLeader
	}

	// if there is no new leader, lock will be released quickly
	rw.mu.Lock()
	defer rw.mu.Unlock()

	// double check
	curLeader := globalLeaderCtrl.getLeaderAddr(rw.key)
	if curLeader == rw.leader {
		return curLeader
	}

	// update leader and reader scs
	rw.leader = curLeader           // curLeader may be "", which means no leader now
	rw.readerScs = rw.readerScs[:0] // reset
	for sc, addr := range rw.scToAddr {
		if addr.Addr != curLeader {
			rw.readerScs = append(rw.readerScs, sc)
		}
	}
	return curLeader
}

func (rw *rwSeparatedRoundBalanced) compareLeader() (bool, string) {
	rw.mu.RLock()
	defer rw.mu.RUnlock()
	cur := globalLeaderCtrl.getLeaderAddr(rw.key)
	return cur == rw.leader, cur
}

const RangeStream = "RangeStream"

func (rw *rwSeparatedRoundBalanced) selectPickFunc(opts balancer.PickOptions) pickerFunc {

	switch opts.FullMethodName {
	case etcdServerRead:
		return rw.pickReader
	case grpcHealthCheck:
		return rw.pickHealthChecker
	case etcdServerWatch:
		if isRangeStream(opts) {
			return rw.pickReader
		}
		return rw.pickWriter
	case etcdServerTxn:
		return rw.pickWriter
	}
	return rw.pickReader
}

func isRangeStream(opts balancer.PickOptions) bool {
	val := opts.Ctx.Value(RangeStream)
	if flag, isBool := val.(bool); isBool && flag {
		return true
	}
	return false
}
