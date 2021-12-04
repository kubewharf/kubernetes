package client

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
