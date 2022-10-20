package qosresourcemanager

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/grpc/metadata"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
	apipod "k8s.io/kubernetes/pkg/api/pod"
	"k8s.io/kubernetes/pkg/apis/core"
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

	if _, exists := pod.Annotations[apipod.TCEDaemonPodAnnotationKey]; exists {
		return true
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

	// customize for tce
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

func GetContextWithSpecificInfo(pod *v1.Pod, container *v1.Container) (context.Context, error) {
	if pod == nil || container == nil {
		return context.Background(), fmt.Errorf("got nil pod: %v or container: %v", pod, container)
	}

	// customize for tce
	// currently we only get psm from pod and may get more infomation later
	psm := pod.Labels[PSMLabelKey]

	if psm == "" {
		return context.Background(), nil
	}

	md := metadata.Pairs("psm", psm)
	return metadata.NewOutgoingContext(context.Background(), md), nil
}

// customize for tce
func canSkipEndpointError(pod *v1.Pod, resource string) bool {
	if pod == nil {
		return false
	}

	// customize for tce
	if pod.Annotations[pluginapi.PodTypeAnnotationKey] == pluginapi.PodTypeBestEffort ||
		IsPodKatalystQoSLevelReclaimedCores(pod) {
		return false
	}

	if IsPodKatalystQoSLevelDedicatedCores(pod) {
		return false
	}

	if IsPodKatalystQoSLevelSystemCores(pod) {
		return false
	}

	if IsPodKatalystQoSLevelSharedCores(pod) || (pod.Annotations[pluginapi.KatalystQoSLevelAnnotationKey] == "" &&
		(pod.Annotations[pluginapi.PodRoleLabelKey] == "" || pod.Annotations[pluginapi.PodTypeAnnotationKey] == pluginapi.PodRoleMicroService)) {
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

	return pod.Annotations[pluginapi.KatalystQoSLevelAnnotationKey] == pluginapi.KatalystQoSLevelSharedCores
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

func DecorateQRMResourceRequest(request *pluginapi.ResourceRequest, pod *v1.Pod, container *v1.Container) error {
	if request == nil {
		return fmt.Errorf("DecorateQRMResourceRequest got nil request")
	} else if pod == nil {
		return fmt.Errorf("DecorateQRMResourceRequest got nil pod")
	} else if container == nil {
		return fmt.Errorf("DecorateQRMResourceRequest got nil container")
	}

	if request.Annotations == nil {
		request.Annotations = make(map[string]string)
	}

	if request.Labels == nil {
		request.Labels = make(map[string]string)
	}

	isSocketPod := false

	for i := range pod.Spec.Containers {
		socketRequest := pod.Spec.Containers[i].Resources.Requests[v1.ResourceName(core.ResourceBytedanceSocket)].DeepCopy()
		socketLimit := pod.Spec.Containers[i].Resources.Limits[v1.ResourceName(core.ResourceBytedanceSocket)].DeepCopy()

		if !socketRequest.IsZero() || !socketLimit.IsZero() {
			isSocketPod = true
			klog.Infof("[DecorateQRMResourceRequest] decorate socket pod: %s/%s container: %s, find container: %s of this pod request socket resources",
				pod.Namespace, pod.Name, container.Name, pod.Spec.Containers[i].Name)
			break
		}
	}

	if isSocketPod {
		if request.Labels[pluginapi.KatalystQoSLevelLabelKey] != pluginapi.KatalystQoSLevelDedicatedCores {
			klog.Warningf("[DecorateQRMResourceRequest] request label key: %s of socket pod: %s/%s container: %s has invalid value: %s, modify its value to: %v",
				pluginapi.KatalystQoSLevelLabelKey, pod.Namespace, pod.Name, container.Name, request.Labels[pluginapi.KatalystQoSLevelLabelKey], pluginapi.KatalystQoSLevelDedicatedCores)
			request.Labels[pluginapi.KatalystQoSLevelLabelKey] = pluginapi.KatalystQoSLevelDedicatedCores
		}

		if request.Annotations[pluginapi.KatalystQoSLevelAnnotationKey] !=
			pluginapi.KatalystQoSLevelDedicatedCores {
			klog.Warningf("[DecorateQRMResourceRequest] request annotation key: %s of socket pod: %s/%s container: %s has invalid value: %s, modify its value to: %v",
				pluginapi.KatalystQoSLevelAnnotationKey, pod.Namespace, pod.Name, container.Name, request.Annotations[pluginapi.KatalystQoSLevelAnnotationKey], pluginapi.KatalystQoSLevelDedicatedCores)
			request.Annotations[pluginapi.KatalystQoSLevelAnnotationKey] = pluginapi.KatalystQoSLevelDedicatedCores
		}

		memoryEnhancement := make(map[string]string)

		json.Unmarshal([]byte(request.Annotations[pluginapi.KatalystMemoryEnhancementAnnotationKey]),
			&memoryEnhancement)

		if memoryEnhancement[pluginapi.KatalystMemoryEnhancementKeyNumaBinding] != pluginapi.KatalystValueTrue {
			klog.Warningf("[DecorateQRMResourceRequest] request memory enhancement key: %s of socket pod: %s/%s container: %s has invalid value: %s, modify its value to: %v",
				pluginapi.KatalystMemoryEnhancementKeyNumaBinding, pod.Namespace, pod.Name, container.Name, memoryEnhancement[pluginapi.KatalystMemoryEnhancementKeyNumaBinding], pluginapi.KatalystValueTrue)
			memoryEnhancement[pluginapi.KatalystMemoryEnhancementKeyNumaBinding] = pluginapi.KatalystValueTrue
			enhancementBytes, _ := json.Marshal(memoryEnhancement)
			request.Annotations[pluginapi.KatalystMemoryEnhancementAnnotationKey] = string(enhancementBytes)
		}
	}

	return nil
}
