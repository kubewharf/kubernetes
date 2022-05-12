package picker

import (
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/resolver"
	"k8s.io/klog/v2"
)

type builder struct {
	logLevel klog.Level
}

func NewBuilder(logLevel klog.Level) base.PickerBuilder {
	return &builder{
		logLevel: logLevel,
	}
}

func (b *builder) Build(readySCs map[resolver.Address]balancer.SubConn) balancer.Picker {
	return newRWSeparatedRoundRobinBalanced(config{
		readySCs: readySCs,
		logLevel: b.logLevel,
	})
}
