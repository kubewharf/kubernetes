package client

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"

	rpc "code.byted.org/tce/kubebrain-client/api/v2rpc"
	"code.byted.org/tce/kubebrain-client/client/balancer/picker"
	iresolver "code.byted.org/tce/kubebrain-client/client/balancer/resolver"
)

type Config struct {
	// Endpoints is a list of URLs
	Endpoints []string `json:"endpoints"`

	// LogLevel is the level of info log
	LogLevel klog.Level `json:"log-level"`

	// MaxCallSendMsgSize is the client-side request send limit in bytes.
	// If 0, it defaults to 2.0 MiB (2 * 1024 * 1024).
	// Make sure that "MaxCallSendMsgSize" < server-side default send/recv limit.
	MaxCallSendMsgSize int `json:"max-call-send-msg-size"`

	// MaxCallRecvMsgSize is the client-side response receive limit.
	// If 0, it defaults to "math.MaxInt32", because range response can
	// easily exceed request send limits.
	// Make sure that "MaxCallRecvMsgSize" >= server-side default send/recv limit.
	MaxCallRecvMsgSize int `json:"max-call-recv-msg-size"`
}

type clientImpl struct {
	config Config

	// cc is a client conn dialed by a client uid, which connect multiple server by resolver
	// and sub conn will be picked with the policy that separates the request of reading and writing
	cc *grpc.ClientConn

	rg iresolver.ResolverGroup

	rpcClient *rpcClient

	ctx    context.Context
	cancel context.CancelFunc
}

type rpcClient struct {
	rpc.ReadClient
	rpc.WriteClient
	rpc.WatchClient
}

func newRpcClient(conn *grpc.ClientConn) *rpcClient {
	c := &rpcClient{}
	c.ReadClient = rpc.NewReadClient(conn)
	c.WriteClient = rpc.NewWriteClient(conn)
	c.WatchClient = rpc.NewWatchClient(conn)
	return c
}

func NewClient(config Config) (Client, error) {
	//config.Endpoints = parseEndpoints(config.Endpoints)
	return newClient(config)
}

func newClient(config Config) (*clientImpl, error) {
	var err error
	c := &clientImpl{config: config}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.rg = iresolver.NewResolverGroup(uuid.New().String())
	c.rg.SetAddrs(config.Endpoints)
	c.cc, err = c.dialWithBalancer(config)
	if err != nil {
		return nil, err
	}

	c.rpcClient = newRpcClient(c.cc)

	lc := newLeaderChecker(config)
	picker.RegisterCluster(config.Endpoints, lc.checkLeader)
	return c, nil
}

func (c *clientImpl) Close() error {
	c.rg.Close()
	_ = c.cc.Close()
	picker.UnregisterCluster(c.config.Endpoints)
	return nil
}

func (c *clientImpl) dialWithBalancer(config Config) (*grpc.ClientConn, error) {
	target := c.rg.Target()

	// grpc msg size dial option
	grpcMsgSizeOpts := []grpc.CallOption{
		defaultMaxCallSendMsgSize,
		defaultMaxCallRecvMsgSize,
	}
	if config.MaxCallSendMsgSize > 0 {
		grpcMsgSizeOpts[0] = grpc.MaxCallSendMsgSize(config.MaxCallSendMsgSize)
	}
	if config.MaxCallRecvMsgSize > 0 {
		grpcMsgSizeOpts[1] = grpc.MaxCallRecvMsgSize(config.MaxCallRecvMsgSize)
	}
	grpcMsgSizeDialOption := grpc.WithDefaultCallOptions(grpcMsgSizeOpts...)

	return c.dial(target, grpc.WithInsecure(), grpc.WithBalancerName(Brain), grpcMsgSizeDialOption)
}

func (c *clientImpl) dial(target string, dops ...grpc.DialOption) (*grpc.ClientConn, error) {
	dialer := iresolver.Dialer
	dops = append(dops, grpc.WithContextDialer(dialer))
	conn, err := grpc.DialContext(c.ctx, target, dops...)
	if err != nil {
		return nil, err
	}
	return conn, nil
}
