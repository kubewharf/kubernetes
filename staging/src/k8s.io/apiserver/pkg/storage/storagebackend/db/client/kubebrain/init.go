package kubebrain

import (
	"google.golang.org/grpc/grpclog"

	"k8s.io/apiserver/pkg/storage/storagebackend/db/client/common"
)

func init() {
	grpclog.SetLoggerV2(common.NewDiscardGRPCLoggerV2())
}
