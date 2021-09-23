/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package nodepackage

import (
	"context"
	"fmt"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/features"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
)

const NodePackageMemMatch = "NodePackageMemoryMatch"

type MatchNodePackageMem struct {
	handle framework.FrameworkHandle
}

var _ = framework.ScorePlugin(&MatchNodePackageMem{})

func NewNodePackageMemMatch(_ *runtime.Unknown, h framework.FrameworkHandle) (framework.Plugin, error) {
	return &MatchNodePackageMem{
		handle: h,
	}, nil
}

func (m *MatchNodePackageMem) Name() string {
	return NodePackageMemMatch
}

// Score invoked at the score extension point.
// TODO: do we need to scoring nodes based on the memory size per numa ?
func (m *MatchNodePackageMem) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	// if feature gate is disable, skip the predicate check
	if !utilfeature.DefaultFeatureGate.Enabled(features.NonNativeResourceSchedulingSupport) {
		return 0, nil
	}

	nodeInfo, err := m.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}

	node := nodeInfo.Node()
	if node == nil {
		return 0, framework.NewStatus(framework.Error, "node not found")
	}

	nodeMemCapacity, ok := node.Status.Capacity[v1.ResourceMemory]
	if !ok {
		return 0, framework.NewStatus(framework.Error, "memory capacity not found in node status")
	}

	nodeNumaCapacity, ok := node.Status.Capacity[v1.ResourceBytedanceSocket]
	if !ok || nodeNumaCapacity.Value() == 0 {
		// if numa info is not reported, do nothing here
		return 0, nil
	}

	memPerNuma := nodeMemCapacity.Value() / nodeNumaCapacity.Value()

	// for now, mem per num can be: 32,48,64,96,128,160,192,256,320,384,512G

	// when we reach here, node capacity must be greater (or equal to) than pod request
	// so, do not need to get pod request
	memShardingUnit, err := resource.ParseQuantity("16Gi")
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("parsing 16Gi error : %v", err))
	}

	shardingValue := memPerNuma / memShardingUnit.Value()

	// shardingValue can be: 2,3,4,6,8,10,12,16,20,24,32
	score := int64(32 - int(shardingValue))
	if score < 0 {
		score = 0
	}

	return score, nil
}

func (m *MatchNodePackageMem) ScoreExtensions() framework.ScoreExtensions {
	return nil
}
