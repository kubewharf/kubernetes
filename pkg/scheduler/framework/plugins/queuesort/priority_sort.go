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

package queuesort

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/api/v1/pod"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
	"k8s.io/kubernetes/pkg/scheduler/util"
)

// Name is the name of the plugin used in the plugin registry and configurations.
const Name = "PrioritySort"

// PrioritySort is a plugin that implements Priority based sorting.
type PrioritySort struct{}

var _ framework.QueueSortPlugin = &PrioritySort{}

// Name returns name of the plugin.
func (pl *PrioritySort) Name() string {
	return Name
}

// Less is the function used by the activeQ heap algorithm to sort pods.
// It sorts pods based on their priority. When priorities are equal, it uses
// PodInfo.timestamp.
func (pl *PrioritySort) Less(pInfo1, pInfo2 *framework.PodInfo) bool {
	p1 := pod.GetPodPriority(pInfo1.Pod)
	p2 := pod.GetPodPriority(pInfo2.Pod)
	if p1 != p2 {
		return p1 > p2
	}

	p1NumaRequest := util.GetPodRequest(pInfo1.Pod, v1.ResourceBytedanceSocket, resource.DecimalSI)
	p2NumaRequest := util.GetPodRequest(pInfo2.Pod, v1.ResourceBytedanceSocket, resource.DecimalSI)
	if p1NumaRequest.Value() != p2NumaRequest.Value() {
		return p1NumaRequest.Value() > p2NumaRequest.Value()
	}

	p1CPURequest := util.GetPodRequest(pInfo1.Pod, v1.ResourceCPU, resource.DecimalSI)
	p2CPURequest := util.GetPodRequest(pInfo2.Pod, v1.ResourceCPU, resource.DecimalSI)
	if p1CPURequest.MilliValue() != p2CPURequest.MilliValue() {
		return p1CPURequest.MilliValue() > p2CPURequest.MilliValue()
	}

	p1MemoryRequest := util.GetPodRequest(pInfo1.Pod, v1.ResourceMemory, resource.BinarySI)
	p2MemoryRequest := util.GetPodRequest(pInfo2.Pod, v1.ResourceMemory, resource.BinarySI)
	if p1MemoryRequest.Value() != p2MemoryRequest.Value() {
		return p1MemoryRequest.Value() > p2MemoryRequest.Value()
	}

	return pInfo1.Timestamp.Before(pInfo2.Timestamp)
}

// New initializes a new plugin and returns it.
func New(plArgs *runtime.Unknown, handle framework.FrameworkHandle) (framework.Plugin, error) {
	return &PrioritySort{}, nil
}
