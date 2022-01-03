package qosresourcemanager

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
	apipod "k8s.io/kubernetes/pkg/api/pod"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager/bitmask"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
)

// with highest precision 0.001
func ParseQuantityToFloat64(quantity resource.Quantity) float64 {
	return float64(quantity.MilliValue()) / 1000.0
}

func ParseTopologyManagerHint(hint topologymanager.TopologyHint) *pluginapi.TopologyHint {
	var nodes []uint64

	if hint.NUMANodeAffinity != nil {
		bits := hint.NUMANodeAffinity.GetBits()

		for _, node := range bits {
			nodes = append(nodes, uint64(node))
		}
	}

	return &pluginapi.TopologyHint{
		Nodes:     nodes,
		Preferred: hint.Preferred,
	}
}

func ParseListOfTopologyHints(hintsList *pluginapi.ListOfTopologyHints) []topologymanager.TopologyHint {
	if hintsList == nil {
		return nil
	}

	resultHints := make([]topologymanager.TopologyHint, 0, len(hintsList.Hints))

	for _, hint := range hintsList.Hints {
		if hint != nil {

			mask := bitmask.NewEmptyBitMask()

			for _, node := range hint.Nodes {
				mask.Add(int(node))
			}

			resultHints = append(resultHints, topologymanager.TopologyHint{
				NUMANodeAffinity: mask,
				Preferred:        hint.Preferred,
			})
		}
	}

	return resultHints
}

func IsInitContainerOfPod(pod *v1.Pod, container *v1.Container) bool {
	if pod == nil || container == nil {
		return false
	}

	n := len(pod.Spec.InitContainers)

	for i := 0; i < n; i++ {
		if pod.Spec.InitContainers[i].Name == container.Name {
			return true
		}
	}

	return false
}

func findContainerIDByName(status *v1.PodStatus, name string) (string, error) {
	allStatuses := status.InitContainerStatuses
	allStatuses = append(allStatuses, status.ContainerStatuses...)
	for _, container := range allStatuses {
		if container.Name == name && container.ContainerID != "" {
			cid := &kubecontainer.ContainerID{}
			err := cid.ParseString(container.ContainerID)
			if err != nil {
				return "", err
			}
			return cid.ID, nil
		}
	}
	return "", fmt.Errorf("unable to find ID for container with name %v in pod status (it may not be running)", name)
}

func isDaemonPod(pod *v1.Pod) bool {
	if _, exists := pod.Annotations[apipod.TCEDaemonPodAnnotationKey]; exists {
		return true
	}

	return false
}

func isSkippedPod(pod *v1.Pod) bool {
	// [TODO](sunjianyu): consider other types of pods need to be skipped
	if pod == nil {
		return true
	}

	return isDaemonPod(pod)
}
