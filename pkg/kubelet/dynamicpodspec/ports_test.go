package dynamicpodspec

import (
	"math/rand"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
)

func Test_getAvailablePorts(t *testing.T) {
	type args struct {
		allocated   map[int]bool
		base        int
		max         int
		arrangeBase int
		portCount   int
		rander      *rand.Rand
	}
	tests := []struct {
		name  string
		args  args
		want  []int
		want1 bool
	}{
		{
			name: "no used ports",
			args: args{
				allocated:   nil,
				base:        9200,
				max:         300,
				arrangeBase: 9200,
				portCount:   2,
				rander:      nil,
			},
			want:  []int{9200, 9201},
			want1: true,
		},
		{
			name: "all ports used",
			args: args{
				allocated: map[int]bool{
					9200: true,
					9201: true,
				},
				base:        9200,
				max:         2,
				arrangeBase: 9201,
				portCount:   2,
				rander:      nil,
			},
			want:  nil,
			want1: false,
		},
		{
			name: "no gap in used ports",
			args: args{
				allocated: map[int]bool{
					9200: true,
					9201: true,
				},
				base:        9200,
				max:         300,
				arrangeBase: 9201,
				portCount:   2,
				rander:      nil,
			},
			want:  []int{9202, 9203},
			want1: true,
		},
		{
			name: "gap in used ports",
			args: args{
				allocated: map[int]bool{
					9200: true,
					9203: true,
				},
				base:        9200,
				max:         300,
				arrangeBase: 9203,
				portCount:   2,
				rander:      nil,
			},
			want:  []int{9204, 9205},
			want1: true,
		},
		{
			name: "no enough port to assign",
			args: args{
				allocated: map[int]bool{
					9200: true,
					9201: true,
				},
				base:        9200,
				max:         5,
				arrangeBase: 9201,
				portCount:   10,
				rander:      nil,
			},
			want:  nil,
			want1: false,
		},
		{
			name: "rand ports",
			args: args{
				allocated:   nil,
				base:        9200,
				max:         50,
				arrangeBase: 9200,
				portCount:   2,
				rander:      rand.New(rand.NewSource(0)),
			},
			want:  []int{9234, 9207},
			want1: true,
		},
		{
			name: "rand ports with used ports",
			args: args{
				allocated: map[int]bool{
					9200: true,
					9201: true,
				},
				base:        9200,
				max:         50,
				arrangeBase: 9201,
				portCount:   2,
				rander:      rand.New(rand.NewSource(0)),
			},
			want:  []int{9224, 9219},
			want1: true,
		},
		{
			name: "no arrange base supplied",
			args: args{
				allocated: map[int]bool{
					9200: true,
					9201: true,
				},
				base:        9200,
				max:         4,
				arrangeBase: 0,
				portCount:   2,
				rander:      nil,
			},
			want:  []int{9202, 9203},
			want1: true,
		},
		{
			name: "big arrange base supplied",
			args: args{
				allocated: map[int]bool{
					9200: true,
					9201: true,
				},
				base:        9200,
				max:         4,
				arrangeBase: 10000,
				portCount:   2,
				rander:      nil,
			},
			want:  []int{9202, 9203},
			want1: true,
		},
		{
			name: "rand ports take all remain ports",
			args: args{
				allocated: map[int]bool{
					9200: true,
					9201: true,
				},
				base:        9200,
				max:         4,
				arrangeBase: 9201,
				portCount:   2,
				rander:      rand.New(rand.NewSource(0)),
			},
			want:  []int{9202, 9203},
			want1: true,
		},
		{
			name: "Ouroboros",
			args: args{
				allocated: map[int]bool{
					9201: true,
					9202: true,
				},
				base:        9200,
				max:         4,
				arrangeBase: 9202,
				portCount:   2,
				rander:      nil,
			},
			want:  []int{9203, 9200},
			want1: true,
		},
		{
			name: "Ouroboros in random mode",
			args: args{
				allocated: map[int]bool{
					9201: true,
					9202: true,
				},
				base:        9200,
				max:         4,
				arrangeBase: 9202,
				portCount:   2,
				rander:      rand.New(rand.NewSource(0)),
			},
			want:  []int{9203, 9200},
			want1: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := getAvailablePorts(transformAllocated(tt.args.allocated), tt.args.base, tt.args.max, tt.args.arrangeBase, tt.args.portCount, tt.args.rander)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getAvailablePorts() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("getAvailablePorts() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func transformAllocated(allocated map[int]bool) map[int]types.UID {
	if allocated == nil {
		return nil
	}

	ret := make(map[int]types.UID)

	for port, ok := range allocated {
		if ok {
			ret[port] = uuid.NewUUID()
		}
	}

	return ret
}
