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
	"math"

	nonnativeresourcelisters "code.byted.org/kubernetes/clientsets/k8s/listers/non.native.resource/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/features"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
	"k8s.io/kubernetes/pkg/scheduler/util"
)

const NodePackageNBWMatch = "NodePackageNBWMatch"

type MatchNodePackageNBW struct {
	handle                    framework.FrameworkHandle
	refinedNodeResourceLister nonnativeresourcelisters.RefinedNodeResourceLister
}

var _ = framework.ScorePlugin(&MatchNodePackageNBW{})

func NewNodePackageNBWMatcher(_ *runtime.Unknown, h framework.FrameworkHandle) (framework.Plugin, error) {
	return &MatchNodePackageNBW{
		handle:                    h,
		refinedNodeResourceLister: h.BytedInformerFactory().Non().V1alpha1().RefinedNodeResources().Lister(),
	}, nil
}

func (m *MatchNodePackageNBW) Name() string {
	return NodePackageNBWMatch
}

// Score invoked at the score extension point.
func (m *MatchNodePackageNBW) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
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

	refinedNode, err := m.refinedNodeResourceLister.Get(node.Name)
	if err != nil {
		// this node has passed predicate checkings, if no refined node resource for it,
		// this may due to pod doesn't request them, so do nothing here.
		return 0, nil
	}

	for _, pattern := range refinedNode.Status.NumericResource.NumericProperties {
		if pattern.PropertyName == util.NBWRefinedResourceKey {
			return pattern.PropertyCapacityValue.Value(), nil
		}
	}

	return 0, nil
}

func (m *MatchNodePackageNBW) ScoreExtensions() framework.ScoreExtensions {
	return m
}

func (m *MatchNodePackageNBW) NormalizeScore(ctx context.Context, state *framework.CycleState, pod *v1.Pod, scores framework.NodeScoreList) *framework.Status {
	var minNbwCapacity int64 = math.MaxInt64
	var maxNbwCapacity int64 = math.MinInt64
	for _, score := range scores {
		if score.Score < minNbwCapacity {
			minNbwCapacity = score.Score
		}
		if score.Score > maxNbwCapacity {
			maxNbwCapacity = score.Score
		}
	}

	for i, score := range scores {
		if maxNbwCapacity == minNbwCapacity {
			scores[i].Score = framework.MinNodeScore
			continue
		}
		newScore := (maxNbwCapacity - score.Score) * framework.MaxNodeScore / (maxNbwCapacity - minNbwCapacity)
		scores[i].Score = newScore
	}
	return nil
}
