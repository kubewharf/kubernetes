package errors

import "errors"

var (
	ErrCompacted = errors.New("compacted")
	ErrNoLeader  = errors.New("no leader")
	ErrTimeout  = errors.New("time out")
)
