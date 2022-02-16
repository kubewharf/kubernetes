package balancesche

import (
	"context"
	"fmt"
	"math"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
	schedulernodeinfo "k8s.io/kubernetes/pkg/scheduler/nodeinfo"
	pluginhelper "k8s.io/kubernetes/pkg/scheduler/framework/plugins/helper"
)

const (
	OVERLOADRATIO = 1.1
)

// BalanceSche currently is a score plugin that favors nodes that with pods that sum up with balanced load Time Seq
type BalanceSche struct {
	handle framework.FrameworkHandle
}

var _ framework.ScorePlugin = &BalanceSche{}
var _ framework.ScoreExtensions = &BalanceSche{}

// Name is the name of the plugin used in the plugin registry and configurations.
const Name = "BalanceSche"

// Name returns name of the plugin. It is used in logs, etc.
func (pl *BalanceSche) Name() string {
	return Name
}

// Score invoked at the score extension point.
func (pl *BalanceSche) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	nodeInfo, err := pl.handle.SnapshotSharedLister().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("getting node %q from Snapshot: %v", nodeName, err))
	}

	score := calculatePriority(nodeInfo, pod)

	return score, nil
}

// ScoreExtensions of the Score plugin.
func (pl *BalanceSche) ScoreExtensions() framework.ScoreExtensions {
	return pl
}

// New initializes a new plugin and returns it.
func New(_ *runtime.Unknown, h framework.FrameworkHandle) (framework.Plugin, error) {
	return &BalanceSche{handle: h}, nil
}

// NormalizeScore invoked after scoring all nodes.
func (pl *BalanceSche) NormalizeScore(ctx context.Context, state *framework.CycleState, pod *v1.Pod, scores framework.NodeScoreList) *framework.Status {
	return pluginhelper.DefaultNormalizeScore(framework.MaxNodeScore, false, scores)
}

func calculatePriority(nodeInfo *schedulernodeinfo.NodeInfo, pod *v1.Pod) int64 {
	nodeLoadCapacity := float64(nodeInfo.AllocatableResource().MilliCPU)/1000.0 * OVERLOADRATIO
	nodeSliceInfo := nodeInfo.GetLoadSliceInfo()
	sliceNum := schedulernodeinfo.GetSliceNum()
	var score float64 = 0
	podSliceInfo, _, err := schedulernodeinfo.GetLoadSliceFromPod(pod)
	if err != nil{
		return 0
	}
	if len(nodeSliceInfo.LoadSlices) != int(sliceNum){
		klog.Errorf("Cannot calculate prioriy with slicenum: %v",len(nodeSliceInfo.LoadSlices))
		return 0
	}
	for k, v := range nodeSliceInfo.LoadSlices {
		podValue, exist := podSliceInfo[k]
		if !exist{
			continue
		}
		resourceGap := nodeLoadCapacity - (v+podValue)
		if resourceGap > 0{
			score += sigmoid(resourceGap)
		}else{
			score -= float64(sliceNum)
		}
	}
	return int64(score) + sliceNum*sliceNum
}

func sigmoid(x float64) float64 {
	return 1 / (1 + math.Pow(math.E, -x/10))
}