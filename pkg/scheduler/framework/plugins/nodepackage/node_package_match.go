package nodepackage

import (
	"context"
	"fmt"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	v1resource "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/features"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
	schedulernodeinfo "k8s.io/kubernetes/pkg/scheduler/nodeinfo"
	"k8s.io/kubernetes/pkg/scheduler/util"
)

const (
	// Name is the name of the plugin used in the plugin registry and configurations.
	Name = "NodePackageMatch"
)

// Args holds the args that are used to configure the plugin.
type Args struct {
	NodePackageResourceMatchFactor *float64 `json:"nodePackageResourceMatchFactor"`
}

type NodePackageMatcher struct {
	nodePackageResourceMatchFactor float64
	// TODO : populate more info, such as metwork bandwidth info (refined node resource crd)
}

// New initializes a new plugin and returns it.
func New(plArgs *runtime.Unknown, _ framework.FrameworkHandle) (framework.Plugin, error) {
	args := &Args{}
	if err := framework.DecodeInto(plArgs, args); err != nil {
		return nil, err
	}

	matcher := &NodePackageMatcher{}
	matcher.nodePackageResourceMatchFactor = *args.NodePackageResourceMatchFactor
	return matcher, nil
}

// TODO: move these error message to error.go file
var ErrWasteNodeCPUResource = fmt.Errorf("NodeMatchesPackage: node(s) does not match package, will waste too much cpu resource")
var ErrWasteNodeMemoryResource = fmt.Errorf("NodeMatchesPackage: node(s) does not match package, will waste too much memory resource")

// Name returns name of the plugin. It is used in logs, etc.
func (matcher *NodePackageMatcher) Name() string {
	return Name
}

func (matcher *NodePackageMatcher) Filter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo *schedulernodeinfo.NodeInfo) *framework.Status {
	// if feature gate is disable, skip the predicate check
	if !utilfeature.DefaultFeatureGate.Enabled(features.NonNativeResourceSchedulingSupport) {
		return nil
	}

	// if annotation is nil or there are no numeric resource requests, skip the predicate check
	if pod.Annotations == nil || len(pod.Annotations[util.NumericResourcesRequests]) == 0 {
		return nil
	}

	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	// get numa num from container request
	requestedNumaNum := v1resource.GetResourceRequest(pod, v1.ResourceBytedanceSocket)

	if requestedNumaNum <= 0 {
		// if no numa request, skip predicate check
		return nil
	}

	cpuRequest, memRequest, _, err := util.ParseCPUMemNetworkRequest(pod.Annotations[util.NumericResourcesRequests])
	if err != nil {
		return framework.NewStatus(framework.Error, fmt.Sprintf("parse cpu, memory, network bandwidth requests error: %v", err))
	}

	if cpuRequest == 0 && memRequest == 0 {
		return nil
	}

	nodeNumaCapacity, ok := node.Status.Capacity[v1.ResourceBytedanceSocket]
	if !ok {
		return framework.NewStatus(framework.Error, "socket capacity not found in node status")
	}

	nodeCPUCapacity, ok := node.Status.Capacity[v1.ResourceCPU]
	if !ok {
		return framework.NewStatus(framework.Error, "cpu capacity not found in node status")
	}
	nodeMemCapacity, ok := node.Status.Capacity[v1.ResourceMemory]
	if !ok {
		return framework.NewStatus(framework.Error, "memory capacity not found in node status")
	}

	// calculate the node cpu/memory capacity based on numa request(versus numa capacity)
	nodeCPUCapacityBasedOnPackageNuma := nodeCPUCapacity.MilliValue() / nodeNumaCapacity.Value() * requestedNumaNum
	nodeMemCapacityBasedOnPackageNuma := nodeMemCapacity.Value() / nodeNumaCapacity.Value() * requestedNumaNum

	// check if node cpu capacity matches package request
	if cpuRequest != 0 && nodeCPUCapacityBasedOnPackageNuma >= int64(float64(cpuRequest)*matcher.nodePackageResourceMatchFactor) {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrWasteNodeCPUResource.Error())
	}

	// check if node memory capacity matches package request
	if memRequest != 0 && nodeMemCapacityBasedOnPackageNuma >= int64(float64(memRequest)*matcher.nodePackageResourceMatchFactor) {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, ErrWasteNodeMemoryResource.Error())
	}

	// TODO: check for network bandwidth
	return nil
}
