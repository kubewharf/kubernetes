package client

import (
	"math"

	"google.golang.org/grpc"
)

var (
	// client-side request send limit, gRPC default is math.MaxInt32
	// Make sure that "client-side send limit < server-side default send/recv limit"
	// Same value as "embed.DefaultMaxRequestBytes" plus gRPC overhead bytes
	defaultMaxCallSendMsgSize = grpc.MaxCallSendMsgSize(2 * 1024 * 1024)

	// client-side response receive limit, gRPC default is 4MB
	// Make sure that "client-side receive limit >= server-side default send/recv limit"
	// because range response can easily exceed request send limits
	// Default to math.MaxInt32; writes exceeding server-side send limit fails anyway
	defaultMaxCallRecvMsgSize = grpc.MaxCallRecvMsgSize(math.MaxInt32)
)

func WithMaxConcurrent(n uint) *withMaxConcurrent {
	return &withMaxConcurrent{maxConcurrent: n}
}

func WithRevision(rev uint64) *withRevision {
	return &withRevision{rev: rev}
}

func WithTTL(ttl int64) *withTTL {
	return &withTTL{ttl: ttl}
}

func WithLimit(limit int64) *withLimit {
	return &withLimit{limit: limit}
}

func WithPrefix() *withPrefix {
	return &withPrefix{}
}

type withTTL struct {
	ttl int64
}

func (w withTTL) decorateUpdateReq(request *UpdateRequest) {
	request.Lease = w.ttl
}

func (w withTTL) decorateCreateReq(request *CreateRequest) {
	request.Lease = w.ttl
}

type withRevision struct {
	rev uint64
}

func (w withRevision) decorateStreamRangeReq(request *RangeStreamRequest) {
	request.Revision = w.rev
}

func (w withRevision) decorateWatchReq(request *WatchRequest) {
	request.Revision = w.rev
}

func (w withRevision) decorateRangeReq(request *RangeRequest) {
	request.Revision = w.rev
}

func (w withRevision) decorateGetReq(request *GetRequest) {
	request.Revision = w.rev
}

func (w withRevision) decorateDeleteReq(request *DeleteRequest) {
	request.Revision = w.rev
}

func (w withRevision) decorateCompactReq(request *CompactRequest) {
	request.Revision = w.rev
}

type withLimit struct {
	limit int64
}

func (w withLimit) decorateRangeReq(request *RangeRequest) {
	request.Limit = w.limit
}

func (w withLimit) decorateStreamRangeReq(request *RangeStreamRequest) {
	request.Limit = w.limit
}

type withPrefix struct{}

func (w withPrefix) decorateWatchReq(request *WatchRequest) {
	request.End = prefixEnd(request.Key)
}

type withMaxConcurrent struct {
	maxConcurrent uint
}

func (w withMaxConcurrent) decorateStreamRangeReq(request *RangeStreamRequest) {
	request.maxConcurrent = w.maxConcurrent
}
