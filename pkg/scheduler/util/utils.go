/*
Copyright 2017 The Kubernetes Authors.

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

package util

import (
	"sort"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/apis/scheduling"
	"k8s.io/kubernetes/pkg/features"
)

// GetContainerPorts returns the used host ports of Pods: if 'port' was used, a 'port:true' pair
// will be in the result; but it does not resolve port conflict.
func GetContainerPorts(pods ...*v1.Pod) []*v1.ContainerPort {
	var ports []*v1.ContainerPort
	for _, pod := range pods {
		for j := range pod.Spec.Containers {
			container := &pod.Spec.Containers[j]
			for k := range container.Ports {
				ports = append(ports, &container.Ports[k])
			}
		}
	}
	return ports
}

// PodPriorityEnabled indicates whether pod priority feature is enabled.
func PodPriorityEnabled() bool {
	return feature.DefaultFeatureGate.Enabled(features.PodPriority)
}

// GetPodFullName returns a name that uniquely identifies a pod.
func GetPodFullName(pod *v1.Pod) string {
	// Use underscore as the delimiter because it is not allowed in pod name
	// (DNS subdomain format).
	return pod.Name + "_" + pod.Namespace
}

// GetPodPriority return priority of the given pod.
func GetPodPriority(pod *v1.Pod) int32 {
	if pod.Spec.Priority != nil {
		return *pod.Spec.Priority
	}
	// When priority of a running pod is nil, it means it was created at a time
	// that there was no global default priority class and the priority class
	// name of the pod was empty. So, we resolve to the static default priority.
	return scheduling.DefaultPriorityWhenNoDefaultClassExists
}

// CanPodBePreempted indicates whether the pod can be preempted
func CanPodBePreempted(pod *v1.Pod) bool {
	if pod.Spec.CanBePreempted == nil {
		return false
	}

	return *pod.Spec.CanBePreempted
}

// PreemptionScopeEqual compares the preemption scope label
// return true if they are equal
func PreemptionScopeEqual(pod1, pod2 *v1.Pod) bool {
	if pod1.Labels == nil || pod2.Labels == nil {
		return false
	}

	if len(pod1.Labels[PreemptionScopeKey]) == 0 || len(pod2.Labels[PreemptionScopeKey]) == 0 {
		return false
	}

	return pod1.Labels[PreemptionScopeKey] == pod2.Labels[PreemptionScopeKey]
}

// SortableList is a list that implements sort.Interface.
type SortableList struct {
	Items    []interface{}
	CompFunc LessFunc
}

// LessFunc is a function that receives two items and returns true if the first
// item should be placed before the second one when the list is sorted.
type LessFunc func(item1, item2 interface{}) bool

var _ = sort.Interface(&SortableList{})

func (l *SortableList) Len() int { return len(l.Items) }

func (l *SortableList) Less(i, j int) bool {
	return l.CompFunc(l.Items[i], l.Items[j])
}

func (l *SortableList) Swap(i, j int) {
	l.Items[i], l.Items[j] = l.Items[j], l.Items[i]
}

// Sort sorts the items in the list using the given CompFunc. Item1 is placed
// before Item2 when CompFunc(Item1, Item2) returns true.
func (l *SortableList) Sort() {
	sort.Sort(l)
}

// HigherPriorityPod return true when priority of the first pod is higher than
// the second one. It takes arguments of the type "interface{}" to be used with
// SortableList, but expects those arguments to be *v1.Pod.
func HigherPriorityPod(pod1, pod2 interface{}) bool {
	p1 := GetPodPriority(pod1.(*v1.Pod))
	p2 := GetPodPriority(pod2.(*v1.Pod))
	if p1 != p2 {
		return p1 > p2
	}

	pod1HasGPU := HasResource(pod1.(*v1.Pod), ResourceGPU)
	pod2HasGPU := HasResource(pod2.(*v1.Pod), ResourceGPU)
	if pod1HasGPU != pod2HasGPU {
		if pod1HasGPU {
			return true
		} else {
			return false
		}
	}

	return true
}

func LessImportantPod(pod1, pod2 interface{}) bool {
	// compare priority first
	p1 := GetPodPriority(pod1.(*v1.Pod))
	p2 := GetPodPriority(pod2.(*v1.Pod))
	if p1 != p2 {
		return p1 < p2
	}
	// if the priorities are equal, compare resource types.
	// order is: GPU, Memory, CPU
	// Since memory and cpu is not that special, put them behind request size comparisons.
	pod1HasGPU := HasResource(pod1.(*v1.Pod), ResourceGPU)
	pod2HasGPU := HasResource(pod2.(*v1.Pod), ResourceGPU)
	if pod1HasGPU != pod2HasGPU {
		if pod1HasGPU {
			return false
		} else {
			return true
		}
	}

	// compare request resource
	// and the resources order is: GPU, Memory, CPU
	// the smaller, the quicker to reprieve
	if pod1HasGPU {
		pod1GPURequest := getPodRequest(pod1.(*v1.Pod), ResourceGPU, resource.DecimalSI)
		pod2GPURequest := getPodRequest(pod2.(*v1.Pod), ResourceGPU, resource.DecimalSI)
		result := pod1GPURequest.Cmp(*pod2GPURequest)
		if result < 0 {
			// pod2 request is greater than pod1,
			// since GPU resource is precious, pod1 is less important
			return true
		} else if result > 0 {
			return false
		}
	}

	pod1MemoryRequest := getPodRequest(pod1.(*v1.Pod), v1.ResourceMemory, resource.BinarySI)
	pod2MemoryRequest := getPodRequest(pod2.(*v1.Pod), v1.ResourceMemory, resource.BinarySI)
	result := pod1MemoryRequest.Cmp(*pod2MemoryRequest)
	if result < 0 {
		// pod2 request is greater than pod1
		return false
	} else if result > 0 {
		return true
	}

	pod1CPURequest := getPodRequest(pod1.(*v1.Pod), v1.ResourceCPU, resource.DecimalSI)
	pod2CPURequest := getPodRequest(pod2.(*v1.Pod), v1.ResourceCPU, resource.DecimalSI)
	result = pod1CPURequest.Cmp(*pod2CPURequest)
	if result < 0 {
		// pod2 request is greater than pod1
		return false
	} else if result > 0 {
		return true
	}

	return true
}

// MoreImportantPod return true when priority of the first pod is higher than
// the second one. It takes arguments of the type "interface{}" to be used with
// SortableList, but expects those arguments to be *v1.Pod.
func MoreImportantPod(pod1, pod2 interface{}) bool {
	// compare priority first
	p1 := GetPodPriority(pod1.(*v1.Pod))
	p2 := GetPodPriority(pod2.(*v1.Pod))
	if p1 != p2 {
		return p1 > p2
	}
	// if the priorities are equal, compare resource types.
	// order is: GPU, Memory, CPU
	// Since memory and cpu is not that special, put them behind request size comparisons.
	pod1HasGPU := HasResource(pod1.(*v1.Pod), ResourceGPU)
	pod2HasGPU := HasResource(pod2.(*v1.Pod), ResourceGPU)
	if pod1HasGPU != pod2HasGPU {
		if pod1HasGPU {
			return true
		} else {
			return false
		}
	}

	// compare request resource
	// and the resources order is: GPU, Memory, CPU
	// the smaller, the quicker to reprieve
	if pod1HasGPU {
		pod1GPURequest := getPodRequest(pod1.(*v1.Pod), ResourceGPU, resource.DecimalSI)
		pod2GPURequest := getPodRequest(pod2.(*v1.Pod), ResourceGPU, resource.DecimalSI)
		result := pod1GPURequest.Cmp(*pod2GPURequest)
		if result < 0 {
			// pod2 request is greater than pod1,
			// since GPU resource is precious, pod2 is more important
			return false
		} else if result > 0 {
			return true
		}
	}

	pod1MemoryRequest := getPodRequest(pod1.(*v1.Pod), v1.ResourceMemory, resource.BinarySI)
	pod2MemoryRequest := getPodRequest(pod2.(*v1.Pod), v1.ResourceMemory, resource.BinarySI)
	result := pod1MemoryRequest.Cmp(*pod2MemoryRequest)
	if result < 0 {
		// pod2 request is greater than pod1, pod1 is more important
		return true
	} else if result > 0 {
		return false
	}

	pod1CPURequest := getPodRequest(pod1.(*v1.Pod), v1.ResourceCPU, resource.DecimalSI)
	pod2CPURequest := getPodRequest(pod2.(*v1.Pod), v1.ResourceCPU, resource.DecimalSI)
	result = pod1CPURequest.Cmp(*pod2CPURequest)
	if result < 0 {
		// pod2 request is greater than pod1, pod1 is more important
		return true
	} else if result > 0 {
		return false
	}

	// TODO: Does CPU and Memory worth this ?
	pod1HasMemory := HasResource(pod1.(*v1.Pod), v1.ResourceMemory)
	pod2HasMemory := HasResource(pod2.(*v1.Pod), v1.ResourceMemory)
	if pod1HasMemory != pod2HasMemory {
		if pod1HasMemory {
			return true
		} else {
			return false
		}
	}

	pod1HasCPU := HasResource(pod1.(*v1.Pod), v1.ResourceCPU)
	pod2HasCPU := HasResource(pod2.(*v1.Pod), v1.ResourceCPU)
	if pod1HasCPU != pod2HasCPU {
		if pod1HasCPU {
			return true
		} else {
			return false
		}
	}

	return true
}

func HasResource(pod *v1.Pod, resourceType v1.ResourceName) bool {
	zeroQuantity := resource.MustParse("0")
	for _, container := range pod.Spec.Containers {
		for key, quantity  := range container.Resources.Requests {
			if key == resourceType && quantity.Cmp(zeroQuantity) == 1 {
				return true
			}
		}
		for key, quantity := range container.Resources.Limits {
			if key == resourceType && quantity.Cmp(zeroQuantity) == 1 {
				return true
			}
		}
	}

	for _, container := range pod.Spec.InitContainers {
		for key, quantity := range container.Resources.Requests {
			if key == resourceType && quantity.Cmp(zeroQuantity) == 1 {
				return true
			}
		}
		for key, quantity := range container.Resources.Limits {
			if key == resourceType && quantity.Cmp(zeroQuantity) == 1 {
				return true
			}
		}
	}

	return false
}

const (
	// hardcode GPU name here
	// TODO: support more GPU names
	ResourceGPU v1.ResourceName = "nvidia.com/gpu"
)

func getPodRequest(pod *v1.Pod, resourceType v1.ResourceName, format resource.Format) *resource.Quantity {
	result := resource.NewQuantity(0, format)
	for _, container := range pod.Spec.Containers {
		for key, value := range container.Resources.Requests {
			if key == resourceType {
				result.Add(value)
			}
		}
	}

	for _, container := range pod.Spec.InitContainers {
		for key, value := range container.Resources.Requests {
			if key == resourceType {
				if result.Cmp(value) < 0 {
					result.SetMilli(value.MilliValue())
				}
			}
		}
	}

	return result
}

const PreemptionScopeKey = "PreemptionScopeKey"
