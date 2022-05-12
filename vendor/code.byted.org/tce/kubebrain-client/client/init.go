package client

import (
	"os"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"k8s.io/klog/v2"

	"code.byted.org/tce/kubebrain-client/client/balancer/picker"
)

const Brain = "brain"

func init() {
	logLevel := 8
	if os.Getenv("DEBUG") != "" {
		logLevel = -1
	}
	balancer.Register(base.NewBalancerBuilder(Brain, picker.NewBuilder(klog.Level(logLevel))))
}
