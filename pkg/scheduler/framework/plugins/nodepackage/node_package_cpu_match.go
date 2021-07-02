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
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/features"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
)

const NodePackageCPUMatch = "NodePackageCPUMatch"

type MatchNodePackageCPU struct {
	handle framework.FrameworkHandle
}

var _ = framework.ScorePlugin(&MatchNodePackageCPU{})

func NewNodePackageCPUMatch(_ *runtime.Unknown, h framework.FrameworkHandle) (framework.Plugin, error) {
	return &MatchNodePackageCPU{
		handle: h,
	}, nil
}

func (m *MatchNodePackageCPU) Name() string {
	return NodePackageCPUMatch
}

// Score invoked at the score extension point.
func (m *MatchNodePackageCPU) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
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

	nodeNumaCapacity, ok := node.Status.Capacity[v1.ResourceBytedanceSocket]
	if !ok || nodeNumaCapacity.Value() == 0 {
		// if numa info is not reported, do nothing here
		return 0, nil
	}

	nodeCPUCapacity, ok := node.Status.Capacity[v1.ResourceCPU]
	if !ok {
		return 0, framework.NewStatus(framework.Error, "cpu capacity not found in node status")
	}

	cpuPerNuma := nodeCPUCapacity.MilliValue() / nodeNumaCapacity.Value()

	score := 1200 * 1000 / cpuPerNuma

	if score >= framework.MaxNodeScore {
		return framework.MaxNodeScore, nil
	} else {
		return score, nil
	}

	/*// now the number of cpus per numa can be: 20,24,32
	// and we won't have new types of machines recently, so hard code here.
	switch {
	case cpuPerNuma <= 20*1000:
		return 10, nil
	case cpuPerNuma <= 24*1000:
		return 5, nil
	case cpuPerNuma <= 32*1000:
		return 1, nil
	default:
		return 0, nil
	}*/
}

func (m *MatchNodePackageCPU) ScoreExtensions() framework.ScoreExtensions {
	return nil
}
