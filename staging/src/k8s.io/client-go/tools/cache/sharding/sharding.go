package sharding

import "hash/fnv"

const (
	DefaultInformerShardingLabelKey = "tce.kubernetes.io/metadata.name"
)

func HashFNV32(s string) int64 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return int64(h.Sum32())
}
