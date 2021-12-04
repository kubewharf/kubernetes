package client

import (
	"context"

	"golang.org/x/sync/errgroup"
)

var noPrefixEnd = []byte{0}

func PrefixEnd(prefix string) string {
	return string(prefixEnd([]byte(prefix)))
}

func prefixEnd(prefix []byte) []byte {
	end := make([]byte, len(prefix))
	copy(end, prefix)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] < 0xff {
			end[i] = end[i] + 1
			end = end[:i+1]
			return end
		}
	}
	// next prefix does not exist (e.g., 0xffff);
	// default to WithFromKey policy
	return noPrefixEnd
}

type Group interface {
	Go(f func() error)
	Wait() error
}

type LimitErrGroup struct {
	ctx     context.Context
	limitCh chan struct{}
	eg      *errgroup.Group
}

func NewConcurrentLimitGroup(ctx context.Context, concurrentMax int) (Group, context.Context) {
	eg, ctx := errgroup.WithContext(ctx)
	if concurrentMax <= 0 {
		return eg, ctx
	}

	limitCh := make(chan struct{}, concurrentMax)
	for i := 0; i < concurrentMax; i++ {
		limitCh <- struct{}{}
	}

	return &LimitErrGroup{
		ctx:     ctx,
		limitCh: limitCh,
		eg:      eg,
	}, ctx
}

func (g *LimitErrGroup) Go(f func() error) {
	select {
	case <-g.limitCh:
	case <-g.ctx.Done():
		return
	}
	defer func() {
		g.limitCh <- struct{}{}
	}()
	g.eg.Go(f)
}

func (g *LimitErrGroup) Wait() error {
	return g.eg.Wait()
}
