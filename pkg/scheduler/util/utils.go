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
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	schedulingv1listers "k8s.io/client-go/listers/scheduling/v1"
	storagev1 "k8s.io/client-go/listers/storage/v1"
	"k8s.io/klog"
	extenderv1 "k8s.io/kube-scheduler/extender/v1"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/kubernetes/pkg/features"
)

// GetPodFullName returns a name that uniquely identifies a pod.
func GetPodFullName(pod *v1.Pod) string {
	// Use underscore as the delimiter because it is not allowed in pod name
	// (DNS subdomain format).
	return pod.Name + "_" + pod.Namespace
}

// GetPodStartTime returns start time of the given pod or current timestamp
// if it hasn't started yet.
func GetPodStartTime(pod *v1.Pod) *metav1.Time {
	if pod.Status.StartTime != nil {
		return pod.Status.StartTime
	}
	// Assumed pods and bound pods that haven't started don't have a StartTime yet.
	return &metav1.Time{Time: time.Now()}
}

// GetEarliestPodStartTime returns the earliest start time of all pods that
// have the highest priority among all victims.
func GetEarliestPodStartTime(victims *extenderv1.Victims) *metav1.Time {
	if len(victims.Pods) == 0 {
		// should not reach here.
		klog.Errorf("victims.Pods is empty. Should not reach here.")
		return nil
	}

	earliestPodStartTime := GetPodStartTime(victims.Pods[0])
	maxPriority := podutil.GetPodPriority(victims.Pods[0])

	for _, pod := range victims.Pods {
		if podutil.GetPodPriority(pod) == maxPriority {
			if GetPodStartTime(pod).Before(earliestPodStartTime) {
				earliestPodStartTime = GetPodStartTime(pod)
			}
		} else if podutil.GetPodPriority(pod) > maxPriority {
			maxPriority = podutil.GetPodPriority(pod)
			earliestPodStartTime = GetPodStartTime(pod)
		}
	}

	return earliestPodStartTime
}

// MoreImportantPod return true when priority of the first pod is higher than
// the second one. If two pods' priorities are equal, compare their StartTime.
// It takes arguments of the type "interface{}" to be used with SortableList,
// but expects those arguments to be *v1.Pod.
func MoreImportantPod(pod1, pod2 *v1.Pod) bool {
	p1 := podutil.GetPodPriority(pod1)
	p2 := podutil.GetPodPriority(pod2)
	if p1 != p2 {
		return p1 > p2
	}
	return GetPodStartTime(pod1).Before(GetPodStartTime(pod2))
}

// GetPodAffinityTerms gets pod affinity terms by a pod affinity object.
func GetPodAffinityTerms(podAffinity *v1.PodAffinity) (terms []v1.PodAffinityTerm) {
	if podAffinity != nil {
		if len(podAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 0 {
			terms = podAffinity.RequiredDuringSchedulingIgnoredDuringExecution
		}
		// TODO: Uncomment this block when implement RequiredDuringSchedulingRequiredDuringExecution.
		//if len(podAffinity.RequiredDuringSchedulingRequiredDuringExecution) != 0 {
		//	terms = append(terms, podAffinity.RequiredDuringSchedulingRequiredDuringExecution...)
		//}
	}
	return terms
}

// GetPodAntiAffinityTerms gets pod affinity terms by a pod anti-affinity.
func GetPodAntiAffinityTerms(podAntiAffinity *v1.PodAntiAffinity) (terms []v1.PodAffinityTerm) {
	if podAntiAffinity != nil {
		if len(podAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 0 {
			terms = podAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
		}
		// TODO: Uncomment this block when implement RequiredDuringSchedulingRequiredDuringExecution.
		//if len(podAntiAffinity.RequiredDuringSchedulingRequiredDuringExecution) != 0 {
		//	terms = append(terms, podAntiAffinity.RequiredDuringSchedulingRequiredDuringExecution...)
		//}
	}
	return terms
}

// HigherPriorityPod return true when priority of the first pod is higher than
// the second one. It takes arguments of the type "interface{}" to be used with
// SortableList, but expects those arguments to be *v1.Pod.
func HigherPriorityPod(pod1, pod2 *v1.Pod) bool {
	p1 := podutil.GetPodPriority(pod1)
	p2 := podutil.GetPodPriority(pod2)
	if p1 != p2 {
		return p1 > p2
	}

	pod1HasGPU := HasResource(pod1, ResourceGPU)
	pod2HasGPU := HasResource(pod2, ResourceGPU)
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
	p1 := podutil.GetPodPriority(pod1.(*v1.Pod))
	p2 := podutil.GetPodPriority(pod2.(*v1.Pod))
	if p1 != p2 {
		return p1 < p2
	}
	// if the priorities are equal, compare resource types.
	// order is: GPU, CPU, Memory
	// Since memory and cpu are not that critical, comparing them after GPU size comparisons.
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
		pod1GPURequest := GetPodRequest(pod1.(*v1.Pod), ResourceGPU, resource.DecimalSI)
		pod2GPURequest := GetPodRequest(pod2.(*v1.Pod), ResourceGPU, resource.DecimalSI)
		result := pod1GPURequest.Cmp(*pod2GPURequest)
		if result < 0 {
			// pod2 request is greater than pod1,
			// since GPU resource is precious, pod1 is less important
			return true
		} else if result > 0 {
			return false
		}
	}

	pod1SocketRequest := GetPodRequest(pod1.(*v1.Pod), v1.ResourceBytedanceSocket, resource.DecimalSI)
	pod2SocketRequest := GetPodRequest(pod2.(*v1.Pod), v1.ResourceBytedanceSocket, resource.DecimalSI)
	result := pod1SocketRequest.Cmp(*pod2SocketRequest)
	if result < 0 {
		return true
	} else if result > 0 {
		return false
	}

	pod1CPURequest := GetPodRequest(pod1.(*v1.Pod), v1.ResourceCPU, resource.DecimalSI)
	pod2CPURequest := GetPodRequest(pod2.(*v1.Pod), v1.ResourceCPU, resource.DecimalSI)
	result = pod1CPURequest.Cmp(*pod2CPURequest)
	if result < 0 {
		// pod2 request is greater than pod1
		return false
	} else if result > 0 {
		return true
	}

	pod1MemoryRequest := GetPodRequest(pod1.(*v1.Pod), v1.ResourceMemory, resource.BinarySI)
	pod2MemoryRequest := GetPodRequest(pod2.(*v1.Pod), v1.ResourceMemory, resource.BinarySI)
	result = pod1MemoryRequest.Cmp(*pod2MemoryRequest)
	if result < 0 {
		// pod2 request is greater than pod1
		return false
	} else if result > 0 {
		return true
	}

	return true
}

func HasResource(pod *v1.Pod, resourceType v1.ResourceName) bool {
	zeroQuantity := resource.MustParse("0")
	for _, container := range pod.Spec.Containers {
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

func GetPodRequest(pod *v1.Pod, resourceType v1.ResourceName, format resource.Format) *resource.Quantity {
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

const deployNameKeyInPodLabels = "name"

// TODO: if we support multiple controller kinds later, we need to get the controller name from reference owners
func GetDeployNameFromPod(pod *v1.Pod) string {
	if pod.Labels != nil {
		return pod.Labels[deployNameKeyInPodLabels]
	}
	return ""
}

func IsRefinedResourceRequest(key string) bool {
	if key == CpuPropertiesRequests || key == GpuPropertiesRequests || key == DiskPropertiesRequests ||
		key == MemoryPropertiesRequests || key == NetworkPropertiesRequests || key == OtherPropertiesRequests ||
		key == NumericResourcesRequests {
		return true
	}

	return false
}

func PodRefinedResourceRequestToString(annotation map[string]string) string {
	result := ""
	if annotation == nil {
		return result
	}
	for key, value := range annotation {
		if IsRefinedResourceRequest(key) {
			result = result + "key: " + key + ", value: " + value + "; "
		}
	}
	return result
}

// check if pod requests refined resources
func PodRequestRefinedResources(pod *v1.Pod) bool {
	if pod.Annotations == nil {
		return false
	}

	if len(pod.Annotations[CpuPropertiesRequests]) > 0 || len(pod.Annotations[GpuPropertiesRequests]) > 0 || len(pod.Annotations[DiskPropertiesRequests]) > 0 ||
		len(pod.Annotations[MemoryPropertiesRequests]) > 0 || len(pod.Annotations[NetworkPropertiesRequests]) > 0 || len(pod.Annotations[OtherPropertiesRequests]) > 0 {
		return true
	}

	if len(pod.Annotations[NumericResourcesRequests]) > 0 {
		return true
	}

	return false
}

func CanPodBePreemptedAtSamePriority(pod, preemptor *v1.Pod, pvcLister corelisters.PersistentVolumeClaimLister, scLister storagev1.StorageClassLister) bool {
	//TODO: add more preemption checking situations
	if IsABPod(pod) {
		// pod is AB test pod, can not be preempted
		return false
	}

	podStateful, err := podutil.IsStatefulPod(pod, pvcLister, scLister)
	if err != nil || podStateful {
		return false
	}
	preemptorStateful, err := podutil.IsStatefulPod(preemptor, pvcLister, scLister)
	if err != nil {
		return false
	} else if preemptorStateful {
		return true
	}

	preemptorHasGPU := HasResource(preemptor, ResourceGPU)
	podHasGPU := HasResource(pod, ResourceGPU)
	if preemptorHasGPU != podHasGPU {
		if preemptorHasGPU {
			return true
		} else {
			return false
		}
	}

	if !utilfeature.DefaultFeatureGate.Enabled(features.NonNativeResourceSchedulingSupport) {
		return false
	}

	// pods request refined resources can preempt those who don't
	preemptorRequestsRefinedResources := PodRequestRefinedResources(preemptor)
	podRequestsRefinedResources := PodRequestRefinedResources(pod)
	if preemptorRequestsRefinedResources != podRequestsRefinedResources {
		if preemptorRequestsRefinedResources {
			return true
		} else {
			return false
		}
	}
	// both pod and preemptor request refined resources or neither requests

	// For now, just check Numa request number
	podNumaRequest := GetPodRequest(pod, v1.ResourceBytedanceSocket, resource.DecimalSI)
	preemptorNumaRequest := GetPodRequest(preemptor, v1.ResourceBytedanceSocket, resource.DecimalSI)

	if preemptorNumaRequest.Value() != podNumaRequest.Value() {
		if preemptorNumaRequest.Value() > podNumaRequest.Value() {
			return true
		} else {
			return false
		}
	}
	// numa requests are equal

	// nbw check
	podCPURequest, podMemRequest, podNetworkRequest, podErr := ParseCPUMemNetworkRequest(pod.Annotations[NumericResourcesRequests])
	preemptorCPURequest, preemptorMemRequest, preemptorNetworkRequest, preemptorErr := ParseCPUMemNetworkRequest(preemptor.Annotations[NumericResourcesRequests])
	if podErr != nil || preemptorErr != nil {
		klog.Errorf("parse cpu, memory, nbw request error, pod error: %v, preemptor error: %v", podErr, preemptorErr)
		return false
	}
	if podNetworkRequest != preemptorNetworkRequest {
		return preemptorNetworkRequest > podNetworkRequest
	}
	// NBW requests are equal

	// large package pods can preempt small package pods
	// at the first stage,
	// if preemptor's cpu request > pod or ( preemptor's cpu request == pod &&  preemptor memory request > pod), then, pod can be preempted
	// TODO: refine this logic
	// only when both pod and preemptor's NumericResourcesRequests are not nil, we do the following check
	if pod.Annotations != nil && len(pod.Annotations[NumericResourcesRequests]) > 0 &&
		preemptor.Annotations != nil && len(preemptor.Annotations[NumericResourcesRequests]) > 0 {
		// we assume cpu and memory are positive correlation in packages
		// podCPURequest > preemptorCPURequest && podMemRequest < preemptorMemRequest should not happen
		if preemptorCPURequest != podCPURequest {
			if preemptorCPURequest > podCPURequest {
				return true
			} else {
				return false
			}
		}

		if preemptorMemRequest != podMemRequest {
			if preemptorMemRequest > podMemRequest {
				return true
			} else {
				return false
			}
		}

	}

	// TODO: add more checks

	return false
}

func ParseCPUMemNetworkRequest(properties string) (int64, int64, int64, error) {
	var cpuRequest, memRequest, networkRequest int64
	var err error
	andProperties := strings.Split(properties, "&")
	for _, andProperty := range andProperties {
		orProperties := strings.Split(andProperty, "|")
		for _, orProperty := range orProperties {
			// TODO: for now, only support ">=" operator
			// support more later if needed
			kv := strings.Split(orProperty, ">=")
			if len(kv) != 2 {
				return 0, 0, 0, fmt.Errorf("properties format error")
			} else {
				// for now, we do not support Z"OR"-package, so there will not be two CPU or Mem requests
				// TODO; if "OR"-package is supported, choose suitable resource requests
				if kv[0] == CPURefinedResourceKey || kv[0] == MemoryRefineResourceKey || kv[0] == NBWRefinedResourceKey {
					request, err := resource.ParseQuantity(kv[1])
					if err != nil {
						return 0, 0, 0, fmt.Errorf("parse quantity for %s error: %v", kv[0], err)
					}
					switch kv[0] {
					case CPURefinedResourceKey:
						cpuRequest = request.MilliValue()
					case MemoryRefineResourceKey:
						memRequest = request.Value()
					case NBWRefinedResourceKey:
						networkRequest = request.Value()
					}
				}
			}
		}
	}

	return cpuRequest, memRequest, networkRequest, err
}

// IsABPod checks if pod is AB test pod
func IsABPod(pod *v1.Pod) bool {
	if pod.Annotations != nil && len(pod.Annotations[ABPodAnnotationKey]) > 0 {
		return true
	}

	return false
}

const (
	// AB test pods annotation key
	ABPodAnnotationKey = "tce.kubernetes.io/ignore-quota"

	// TODO: using Annotations at the first stage, modify API later if needed

	// Annotation keys for refined resource

	// discrete resource keys,
	// TODO: combine them into one. json format, such as: {"DiscreteResourcesKeys": {"CpuProperties": "k1=v1|k1=v2&k3=v3","GpuProperties":"k1=v1|k1=v2&k3=v3" }}
	CpuPropertiesRequests     = "CpuPropertiesRequests"
	GpuPropertiesRequests     = "GpuPropertiesRequests"
	DiskPropertiesRequests    = "DiskPropertiesRequests"
	MemoryPropertiesRequests  = "MemoryPropertiesRequests"
	NetworkPropertiesRequests = "NetworkPropertiesRequests"
	OtherPropertiesRequests   = "OtherPropertiesRequests"

	// numeric resource keys
	// one can be consumed and the other can not
	// For now, we only support numeric resources that can not be consumed
	// TODO: consider using NodeAffinity ?
	// TODO: support numeric resource that can be consumed
	// Format can be: "NumericResourcesKeys":"MBM>=200"
	NumericResourcesRequests = "NumericResourcesRequests"

	// TODO: numa/socket application and it also have CPU and Memory requirement,
	// for example: instance CPU > 64C, instance Memory > 64G
	// store Node CPU, Memory and numa/socket info(allocatable and capacity) in CRDs and take them into account
)

const (
	CPURefinedResourceKey   = "cpu"
	MemoryRefineResourceKey = "memory"
	NumaRefinedResourceKey  = "numa"
	NBWRefinedResourceKey   = "nbw"

	SocketToCpuKey            = "sockettocpu"
	PodDebugModeAnnotationKey = "debug-mode"

	CanBePreemptedAnnotationKey = "tce.kubernetes.io/can-be-preempted"
)

// CanPodBePreempted indicates whether the pod can be preempted
func CanPodBePreempted(pod *v1.Pod, pcLister schedulingv1listers.PriorityClassLister) bool {
	if pod.Annotations == nil || len(pod.Annotations[CanBePreemptedAnnotationKey]) == 0 {
		if len(pod.Spec.PriorityClassName) > 0 {
			sc, err := pcLister.Get(pod.Spec.PriorityClassName)
			if err != nil {
				klog.Infof("get sc error: %v", err)
				return false
			}
			return sc.Annotations != nil && sc.Annotations[CanBePreemptedAnnotationKey] == "true"
		}
	}

	return pod.Annotations != nil && pod.Annotations[CanBePreemptedAnnotationKey] == "true"
}

const (
	TCEDaemonPodAnnotation = "pod.tce.kubernetes.io/tce-daemon"
)

func IsTCEDaemonPod(pod *v1.Pod) bool {
	if pod == nil || pod.Annotations == nil {
		return false
	}
	if _, ok := pod.Annotations[TCEDaemonPodAnnotation]; ok {
		return true
	}

	return false
}

const (
	PSMLabel = "psm"
)

func GetPSMFromPod(pod *v1.Pod) string {
	return pod.Labels[PSMLabel]
}

// GetUpdatedPod returns the latest version of <pod> from API server.
func GetUpdatedPod(cs kubernetes.Interface, pod *v1.Pod) (*v1.Pod, error) {
	return cs.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
}

// DeletePod deletes the given <pod> from API server
func DeletePod(cs kubernetes.Interface, pod *v1.Pod) error {
	return cs.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
}

// PatchPodStatus calculates the delta bytes change from <old.Status> to <newStatus>,
// and then submit a request to API server to patch the pod changes.
func PatchPodStatus(cs kubernetes.Interface, old *v1.Pod, newStatus *v1.PodStatus) error {
	if newStatus == nil {
		return nil
	}

	oldData, err := json.Marshal(v1.Pod{Status: old.Status})
	if err != nil {
		return err
	}

	newData, err := json.Marshal(v1.Pod{Status: *newStatus})
	if err != nil {
		return err
	}
	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, &v1.Pod{})
	if err != nil {
		return fmt.Errorf("failed to create merge patch for pod %q/%q: %v", old.Namespace, old.Name, err)
	}
	_, err = cs.CoreV1().Pods(old.Namespace).Patch(context.TODO(), old.Name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{}, "status")
	return err
}

// ClearNominatedNodeName internally submit a patch request to API server
// to set each pods[*].Status.NominatedNodeName> to "".
func ClearNominatedNodeName(cs kubernetes.Interface, pods ...*v1.Pod) utilerrors.Aggregate {
	var errs []error
	for _, p := range pods {
		if len(p.Status.NominatedNodeName) == 0 {
			continue
		}
		podStatusCopy := p.Status.DeepCopy()
		podStatusCopy.NominatedNodeName = ""
		if err := PatchPodStatus(cs, p, podStatusCopy); err != nil {
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}
