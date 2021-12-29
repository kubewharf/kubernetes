package util

import (
	"fmt"
	"strconv"
	"time"

	"k8s.io/api/core/v1"
)

const (
	ResourceGPUMemName   = "bytedance.com/gpu-memory"
	ResourceGPUSMName    = "bytedance.com/gpu-sm"
	ResourceShareGPUName = "nvidia.com/share-gpu"

	ShareGPUIDIndex               = "tce.kubernetes.io/gpu-id"
	ShareGPUAssumedTimeAnnotation = "tce.kubernetes.io/gpu-share-assumed-time"
)

// Is the Node for GPU sharing
func IsGPUSharingNode(node *v1.Node) bool {
	return GetGPUCountInNode(node) > 0
}

// Get the total share GPU of the Node
func GetTotalShareGPU(node *v1.Node) int {
	val, ok := node.Status.Allocatable[ResourceShareGPUName]

	if !ok {
		return 0
	}

	return int(val.Value())
}

// Get the total GPU memory of the Node
func GetTotalGPUMemory(node *v1.Node) int {
	val, ok := node.Status.Allocatable[ResourceGPUMemName]

	if !ok {
		return 0
	}

	return int(val.Value())
}

// Get the total GPU memory of the Node
func GetTotalGPUSM(node *v1.Node) int {
	val, ok := node.Status.Allocatable[ResourceGPUSMName]

	if !ok {
		return 0
	}

	return int(val.Value())
}

// Get the GPU count of the node
func GetGPUCountInNode(node *v1.Node) int {
	val, ok := node.Status.Allocatable[ResourceGPUSMName]

	if ok {
		return int(val.Value()) / 10
	}
	val, ok = node.Status.Allocatable[ResourceShareGPUName]
	if ok {
		return int(val.Value()) / 1024
	}
	return 0
}

// IsCompletePod determines if the pod is complete
func IsCompletePod(pod *v1.Pod) bool {
	if pod.DeletionTimestamp != nil {
		return true
	}

	if pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed {
		return true
	}
	return false
}

// IsGPUSharingPod determines if it's the pod for GPU sharing
func IsGPUSharingPod(pod *v1.Pod) bool {
	return GetGPUMemoryFromPodResource(pod) > 0 || GetGPUSMFromPodResource(pod) > 0 || GetShareGPUFromPodResource(pod) > 0
}

// GetGPUIDFromAnnotation gets GPU ID from Annotation
func GetGPUIDFromAnnotation(pod *v1.Pod) int {
	if len(pod.Annotations) > 0 {
		// parse tce.kubernetes.io/gpu-id anntation to get physical GPU ID
		if value, found := pod.Annotations[ShareGPUIDIndex]; found {
			if id, err := strconv.Atoi(value); err == nil {
				return id
			}
		}
	}
	return -1
}

// GetGPUMemoryFromPodResource gets GPU Memory of the Pod
func GetGPUMemoryFromPodResource(pod *v1.Pod) int {
	var total int
	containers := pod.Spec.Containers
	for _, container := range containers {
		if val, ok := container.Resources.Limits[ResourceGPUMemName]; ok {
			total += int(val.Value())
		}
	}
	return total
}

// GetGPUSMFromPodResource gets GPU SM of the Pod
func GetGPUSMFromPodResource(pod *v1.Pod) int {
	var total int
	containers := pod.Spec.Containers
	for _, container := range containers {
		if val, ok := container.Resources.Limits[ResourceGPUSMName]; ok {
			total += int(val.Value())
		}
	}
	return total
}

// GetGPUMemoryFromPodResource gets GPU Memory of the Pod
func GetShareGPUFromPodResource(pod *v1.Pod) int {
	var total int
	containers := pod.Spec.Containers
	for _, container := range containers {
		if val, ok := container.Resources.Limits[ResourceShareGPUName]; ok {
			total += int(val.Value())
		}
	}
	return total
}

// AddPodAnnotation updates pod env with devId
func AddPodAnnotation(pod *v1.Pod, devId int) {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	pod.Annotations[ShareGPUIDIndex] = fmt.Sprintf("%d", devId)
	pod.Annotations[ShareGPUAssumedTimeAnnotation] = time.Now().Format("2006-01-02 15:04:05")
}
