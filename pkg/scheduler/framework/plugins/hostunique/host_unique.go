package hostunique

import (
	"context"
	"fmt"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
	apipod "k8s.io/kubernetes/pkg/api/pod"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
	schedulernodeinfo "k8s.io/kubernetes/pkg/scheduler/nodeinfo"
	schedutil "k8s.io/kubernetes/pkg/scheduler/util"
	"strconv"
)

var (
	ErrPodHostUniqueRulesNotMatch = fmt.Errorf("PodHostUniqueRulesNotMatch: node(s) didn't match pod host unique rules")
	ErrPodAffinityNotMatch        = fmt.Errorf("MatchInterPodAffinity: node(s) didn't match pod affinity/anti-affinity")
)

// Name is the name of the plugin used in the plugin registry and configurations.
const Name = "MatchHostUnique"

type HostUniqueChecker struct {
	handle framework.FrameworkHandle
}

func (c *HostUniqueChecker) Name() string {
	return Name
}

func New(_ *runtime.Unknown, handle framework.FrameworkHandle) (framework.Plugin, error) {
	c := &HostUniqueChecker{
		handle: handle,
	}
	return c, nil
}

func (c *HostUniqueChecker) Predicate(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo *schedulernodeinfo.NodeInfo) *framework.Status {
	if nodeInfo.Node() == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	// Now check if <pod> requirements will be satisfied on this node.
	affinity := pod.Spec.Affinity
	if affinity == nil || affinity.PodAntiAffinity == nil {
		return nil
	}
	if failedPredicates := c.satisfiesPodsHostUnique(pod, nodeInfo, affinity); failedPredicates != nil {
		failedPredicates := append([]string{ErrPodAffinityNotMatch.Error()}, failedPredicates.Error())
		return framework.NewStatus(framework.Unschedulable, failedPredicates...)
	}

	if klog.V(10) {
		// We explicitly don't do klog.V(10).Infof() to avoid computing all the parameters if this is
		// not logged. There is visible performance gain from it.
		klog.Infof("Schedule Pod %+v on Node %+v is allowed, pod (anti)affinity constraints satisfied",
			podName(pod), nodeInfo.Node().Name)
	}
	return nil
}

func (c *HostUniqueChecker) satisfiesPodsHostUnique(pod *v1.Pod, nodeInfo *schedulernodeinfo.NodeInfo, affinity *v1.Affinity) error {
	for _, term := range schedutil.GetPodAntiAffinityTerms(affinity.PodAntiAffinity) {
		termMatches, err := c.anyPodMatchesPodAffinityTerm(pod, nodeInfo, &term)
		if err != nil || termMatches {
			klog.V(10).Infof("Cannot schedule pod %+v onto node %v, because of PodAntiAffinityTerm %v, err: %v",
				podName(pod), nodeInfo.Node().Name, term, err)
			return ErrPodHostUniqueRulesNotMatch
		}
	}
	return nil
}

func (c *HostUniqueChecker) anyPodMatchesPodAffinityTerm(pod *v1.Pod, nodeInfo *schedulernodeinfo.NodeInfo, term *v1.PodAffinityTerm) (bool, error) {
	if len(term.TopologyKey) == 0 {
		return false, fmt.Errorf("empty topologyKey is not allowed except for PreferredDuringScheduling pod anti-affinity")
	}
	if term.TopologyKey != v1.LabelHostname {
		return true, nil
	}
	toleranced := 1
	toleranceCount := 1
	anno := pod.GetAnnotations()
	if anno != nil {
		if v, ok := anno[apipod.PodHostUniqueToleranceAnnotation]; ok {
			if c, err := strconv.Atoi(v); err == nil {
				toleranceCount = c
			}
		}
	}
	namespaces := schedutil.GetNamespacesFromPodAffinityTerm(pod, term)
	selector, err := metav1.LabelSelectorAsSelector(term.LabelSelector)
	if err != nil {
		return false, err
	}
	for _, existingPod := range nodeInfo.Pods() {
		if existingPod.DeletionTimestamp != nil {
			continue
		}
		if schedutil.PodMatchesTermsNamespaceAndSelector(existingPod, namespaces, selector) {
			toleranced++
			if toleranced > toleranceCount {
				return true, nil
			}
		}

	}
	return false, nil
}

func podName(pod *v1.Pod) string {
	return pod.Namespace + "/" + pod.Name
}
