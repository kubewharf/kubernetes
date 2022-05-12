package common

import (
	"fmt"

	"google.golang.org/grpc/grpclog"
	"k8s.io/klog"
)

func NewDiscardGRPCLoggerV2() grpclog.LoggerV2 {
	return grpclog.NewLoggerV2(discard{}, discard{}, discard{})
}

type discard struct{}

func (discard) Write(p []byte) (int, error) {
	return len(p), nil
}

func (discard) WriteString(s string) (int, error) {
	return len(s), nil
}

func NewGRPCLoggerV2() grpclog.LoggerV2 {
	return klogWrapper{}
}

type klogWrapper struct{}

const klogWrapperDepth = 4

func (klogWrapper) Info(args ...interface{}) {
	klog.InfoDepth(klogWrapperDepth, args...)
}

func (klogWrapper) Infoln(args ...interface{}) {
	klog.InfoDepth(klogWrapperDepth, fmt.Sprintln(args...))
}

func (klogWrapper) Infof(format string, args ...interface{}) {
	klog.InfoDepth(klogWrapperDepth, fmt.Sprintf(format, args...))
}

func (klogWrapper) Warning(args ...interface{}) {
	klog.WarningDepth(klogWrapperDepth, args...)
}

func (klogWrapper) Warningln(args ...interface{}) {
	klog.WarningDepth(klogWrapperDepth, fmt.Sprintln(args...))
}

func (klogWrapper) Warningf(format string, args ...interface{}) {
	klog.WarningDepth(klogWrapperDepth, fmt.Sprintf(format, args...))
}

func (klogWrapper) Error(args ...interface{}) {
	klog.ErrorDepth(klogWrapperDepth, args...)
}

func (klogWrapper) Errorln(args ...interface{}) {
	klog.ErrorDepth(klogWrapperDepth, fmt.Sprintln(args...))
}

func (klogWrapper) Errorf(format string, args ...interface{}) {
	klog.ErrorDepth(klogWrapperDepth, fmt.Sprintf(format, args...))
}

func (klogWrapper) Fatal(args ...interface{}) {
	klog.FatalDepth(klogWrapperDepth, args...)
}

func (klogWrapper) Fatalln(args ...interface{}) {
	klog.FatalDepth(klogWrapperDepth, fmt.Sprintln(args...))
}

func (klogWrapper) Fatalf(format string, args ...interface{}) {
	klog.FatalDepth(klogWrapperDepth, fmt.Sprintf(format, args...))
}

func (klogWrapper) V(l int) bool {
	return bool(klog.V(klog.Level(l)))
}
