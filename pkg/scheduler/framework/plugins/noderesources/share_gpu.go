package noderesources

import (
	"context"
	"fmt"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/features"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
	schedulernodeinfo "k8s.io/kubernetes/pkg/scheduler/nodeinfo"
	"k8s.io/kubernetes/pkg/scheduler/util"
)

// ShareGPU contains information to check share gpu.
type ShareGPU struct {
}

var _ = framework.FilterPlugin(&ShareGPU{})

// LeastAllocatedName is the name of the plugin used in the plugin registry and configurations.
const ShareGPUName = "ShareGPU"

// Name returns name of the plugin. It is used in logs, etc.
func (sg *ShareGPU) Name() string {
	return ShareGPUName
}

// This predicate checks if shared gpu left on single GPU in the specific node satisfies the pod's request
func NewShareGPU(_ *runtime.Unknown, h framework.FrameworkHandle) (framework.Plugin, error) {
	return &ShareGPU{}, nil
}

// Filter invoked at the filter extension point.
func (sg *ShareGPU) Filter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo *schedulernodeinfo.NodeInfo) *framework.Status {
	if nodeInfo == nil || nodeInfo.Node() == nil {
		return framework.NewStatus(framework.Error, "invalid nodeInfo")
	}

	// if share gpu feature gate is not enabled, just return
	if !utilfeature.DefaultFeatureGate.Enabled(features.ShareGPU) {
		return nil
	}

	// if pod resource has no requirement for share gpu (including gpu-sm and gpu memory), just return
	if !util.IsGPUSharingPod(pod) {
		return nil
	}

	gpuSatisfied := nodeInfo.NodeShareGPUDeviceInfo.SatisfyShareGPU(pod)
	if gpuSatisfied {
		return nil
	} else {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, fmt.Sprintf("node %s has no sufficient share gpu", nodeInfo.Node().Name))
	}
}
