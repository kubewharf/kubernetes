package qosresourcemanager

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
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
	if status == nil {
		return "", fmt.Errorf("findContainerIDByName got nil status")
	}

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
	if pod == nil {
		return false
	}

	for i := 0; i < len(pod.OwnerReferences); i++ {
		if pod.OwnerReferences[i].Kind == DaemonsetKind {
			return true
		}
	}

	return false
}

// [TODO]: to discuss use katalyst qos level or daemon label to skip pods
func isSkippedPod(pod *v1.Pod, isFirstAdmit bool) bool {
	// [TODO](sunjianyu): consider other types of pods need to be skipped
	if pod == nil {
		return true
	}

	if isFirstAdmit && IsPodSkipFirstAdmit(pod) {
		return true
	}

	return isDaemonPod(pod) && !IsPodKatalystQoSLevelSystemCores(pod)
}

func isSkippedContainer(pod *v1.Pod, container *v1.Container) bool {
	// [TODO](sunjianyu):
	// 1. we skip init container currently and if needed we should implement reuse strategy later
	// 2. consider other types of containers need to be skipped
	containerType, _, err := GetContainerTypeAndIndex(pod, container)

	if err != nil {
		klog.Errorf("GetContainerTypeAndIndex failed with error: %v", err)
		return false
	}

	return containerType == pluginapi.ContainerType_INIT
}

func GetContainerTypeAndIndex(pod *v1.Pod, container *v1.Container) (containerType pluginapi.ContainerType, containerIndex uint64, err error) {
	if pod == nil || container == nil {
		err = fmt.Errorf("got nil pod: %v or container: %v", pod, container)
		return
	}

	foundContainer := false

	for i, initContainer := range pod.Spec.InitContainers {
		if container.Name == initContainer.Name {
			foundContainer = true
			containerType = pluginapi.ContainerType_INIT
			containerIndex = uint64(i)
			break
		}
	}

	if !foundContainer {
		mainContainerName := pod.Annotations[MainContainerNameAnnotationKey]

		if mainContainerName == "" && len(pod.Spec.Containers) > 0 {
			mainContainerName = pod.Spec.Containers[0].Name
		}

		for i, appContainer := range pod.Spec.Containers {
			if container.Name == appContainer.Name {
				foundContainer = true

				if container.Name == mainContainerName {
					containerType = pluginapi.ContainerType_MAIN
				} else {
					containerType = pluginapi.ContainerType_SIDECAR
				}

				containerIndex = uint64(i)
				break
			}
		}
	}

	if !foundContainer {
		err = fmt.Errorf("GetContainerTypeAndIndex doesn't find container: %s in pod: %s/%s", container.Name, pod.Namespace, pod.Name)
	}

	return
}

func canSkipEndpointError(pod *v1.Pod, resource string) bool {
	if pod == nil {
		return false
	}

	if IsPodKatalystQoSLevelReclaimedCores(pod) {
		return false
	}

	if IsPodKatalystQoSLevelDedicatedCores(pod) {
		return false
	}

	if IsPodKatalystQoSLevelSystemCores(pod) {
		return false
	}

	if IsPodKatalystQoSLevelSharedCores(pod) {
		return true
	}

	return false
}

func IsPodKatalystQoSLevelDedicatedCores(pod *v1.Pod) bool {
	if pod == nil {
		return false
	}

	return pod.Annotations[pluginapi.KatalystQoSLevelAnnotationKey] == pluginapi.KatalystQoSLevelDedicatedCores
}

func IsPodKatalystQoSLevelSharedCores(pod *v1.Pod) bool {
	if pod == nil {
		return false
	}

	return pod.Annotations[pluginapi.KatalystQoSLevelAnnotationKey] == pluginapi.KatalystQoSLevelSharedCores ||
		pod.Annotations[pluginapi.KatalystQoSLevelAnnotationKey] == ""
}

func IsPodKatalystQoSLevelReclaimedCores(pod *v1.Pod) bool {
	if pod == nil {
		return false
	}

	return pod.Annotations[pluginapi.KatalystQoSLevelAnnotationKey] == pluginapi.KatalystQoSLevelReclaimedCores
}

func IsPodKatalystQoSLevelSystemCores(pod *v1.Pod) bool {
	if pod == nil {
		return false
	}

	return pod.Annotations[pluginapi.KatalystQoSLevelAnnotationKey] == pluginapi.KatalystQoSLevelSystemCores
}

func IsPodSkipFirstAdmit(pod *v1.Pod) bool {
	if pod == nil {
		return false
	}

	return pod.Annotations[pluginapi.KatalystSkipQRMAdmitAnnotationKey] == pluginapi.KatalystValueTrue
}
