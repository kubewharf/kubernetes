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
	nodeInfo, err := m.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}

	node := nodeInfo.Node()
	if node == nil {
		return 0, framework.NewStatus(framework.Error, "node not found")
	}

	// if feature gate is disable, skip the predicate check
	if !utilfeature.DefaultFeatureGate.Enabled(features.NonNativeResourceSchedulingSupport) {
		return 0, nil
	}

	nodeCPUCapacity, ok := node.Status.Capacity[v1.ResourceCPU]
	if !ok {
		return 0, framework.NewStatus(framework.Error, "cpu capacity not found in node status")
	}

	// when we reach here, node capacity must be greater (or equal to) than pod request
	// so, do not need to get pod request
	if nodeCPUCapacity.MilliValue() <= 40*1000 {
		return 10, nil
	} else if nodeCPUCapacity.MilliValue() <= 48*1000 {
		return 8, nil
	} else {
		return 1, nil
	}
}

func (m *MatchNodePackageCPU) ScoreExtensions() framework.ScoreExtensions {
	return nil
}
