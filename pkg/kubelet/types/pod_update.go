/*
Copyright 2014 The Kubernetes Authors.

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

package types

import (
	"fmt"
	"strconv"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/apis/scheduling"
)

const (
	ConfigSourceAnnotationKey    = "kubernetes.io/config.source"
	ConfigMirrorAnnotationKey    = v1.MirrorPodAnnotationKey
	ConfigFirstSeenAnnotationKey = "kubernetes.io/config.seen"
	ConfigHashAnnotationKey      = "kubernetes.io/config.hash"

	// SriovDeviceAnnotation determines the PCIe Address of Allocated VF
	SriovDeviceAnnotation = "io.kubernetes.network.switchdev.pciaddress"

	CriticalPodAnnotationKey               = "scheduler.alpha.kubernetes.io/critical-pod"
	CriticalTCEPodAnnotationKey            = "scheduler.alpha.kubernetes.io/tce-critical-pod"
	DaemonIncludeNotReadyNodeAnnotationKey = "scheduler.alpha.kubernetes.io/daemon-include-not-ready-node"
)

const (
	RequestCpuUserDemandAnnotationKey         = "pod.tce.kubernetes.io/requestCpuUserDemand"
	RequestMemUserDemandAnnotationKey         = "pod.tce.kubernetes.io/requestMemUserDemand"
	RequestCpuCfsShareBurstRatioAnnotationKey = "pod.tce.kubernetes.io/requestCpuCfsShareBurstRatio"
	RequestMemCfsShareBurstRatioAnnotationKey = "pod.tce.kubernetes.io/requestMemCfsShareBurstRatio"
	LimitCpuUserDemandAnnotationKey           = "pod.tce.kubernetes.io/limitCpuUserDemand"
	LimitMemUserDemandAnnotationKey           = "pod.tce.kubernetes.io/limitMemUserDemand"
	LimitCpuCfsQuotaPeriodAnnotationKey       = "pod.tce.kubernetes.io/limitCpuCfsQuotaPeriod"

	ContainerShmSizeAnnotationKey   = "pod.tce.kubernetes.io/container-shm-size"
	ContainerPidsLimitAnnotationKey = "pod.tce.kubernetes.io/container-pids-limit"
)

// PodOperation defines what changes will be made on a pod configuration.
type PodOperation int

const (
	// This is the current pod configuration
	SET PodOperation = iota
	// Pods with the given ids are new to this source
	ADD
	// Pods with the given ids are gracefully deleted from this source
	DELETE
	// Pods with the given ids have been removed from this source
	REMOVE
	// Pods with the given ids have been updated in this source
	UPDATE
	// Pods with the given ids have unexpected status in this source,
	// kubelet should reconcile status with this source
	RECONCILE
	// Pods with the given ids have been restored from a checkpoint.
	RESTORE

	// These constants identify the sources of pods
	// Updates from a file
	FileSource = "file"
	// Updates from querying a web page
	HTTPSource = "http"
	// Updates from Kubernetes API Server
	ApiserverSource = "api"
	// Updates from all sources
	AllSource = "*"

	NamespaceDefault = metav1.NamespaceDefault
)

// PodUpdate defines an operation sent on the channel. You can add or remove single services by
// sending an array of size one and Op == ADD|REMOVE (with REMOVE, only the ID is required).
// For setting the state of the system to a given state for this source configuration, set
// Pods as desired and Op to SET, which will reset the system state to that specified in this
// operation for this source channel. To remove all pods, set Pods to empty object and Op to SET.
//
// Additionally, Pods should never be nil - it should always point to an empty slice. While
// functionally similar, this helps our unit tests properly check that the correct PodUpdates
// are generated.
type PodUpdate struct {
	Pods   []*v1.Pod
	Op     PodOperation
	Source string
}

// Gets all validated sources from the specified sources.
func GetValidatedSources(sources []string) ([]string, error) {
	validated := make([]string, 0, len(sources))
	for _, source := range sources {
		switch source {
		case AllSource:
			return []string{FileSource, HTTPSource, ApiserverSource}, nil
		case FileSource, HTTPSource, ApiserverSource:
			validated = append(validated, source)
		case "":
			// Skip
		default:
			return []string{}, fmt.Errorf("unknown pod source %q", source)
		}
	}
	return validated, nil
}

// GetPodSource returns the source of the pod based on the annotation.
func GetPodSource(pod *v1.Pod) (string, error) {
	if pod.Annotations != nil {
		if source, ok := pod.Annotations[ConfigSourceAnnotationKey]; ok {
			return source, nil
		}
	}
	return "", fmt.Errorf("cannot get source of pod %q", pod.UID)
}

// SyncPodType classifies pod updates, eg: create, update.
type SyncPodType int

const (
	// SyncPodSync is when the pod is synced to ensure desired state
	SyncPodSync SyncPodType = iota
	// SyncPodUpdate is when the pod is updated from source
	SyncPodUpdate
	// SyncPodCreate is when the pod is created from source
	SyncPodCreate
	// SyncPodKill is when the pod is killed based on a trigger internal to the kubelet for eviction.
	// If a SyncPodKill request is made to pod workers, the request is never dropped, and will always be processed.
	SyncPodKill
)

func (sp SyncPodType) String() string {
	switch sp {
	case SyncPodCreate:
		return "create"
	case SyncPodUpdate:
		return "update"
	case SyncPodSync:
		return "sync"
	case SyncPodKill:
		return "kill"
	default:
		return "unknown"
	}
}

// IsMirrorPod returns true if the passed Pod is a Mirror Pod.
func IsMirrorPod(pod *v1.Pod) bool {
	_, ok := pod.Annotations[ConfigMirrorAnnotationKey]
	return ok
}

// IsStaticPod returns true if the pod is a static pod.
func IsStaticPod(pod *v1.Pod) bool {
	source, err := GetPodSource(pod)
	return err == nil && source != ApiserverSource
}

// IsCriticalPod returns true if pod's priority is greater than or equal to SystemCriticalPriority.
func IsCriticalPod(pod *v1.Pod) bool {
	if IsStaticPod(pod) {
		return true
	}
	if IsMirrorPod(pod) {
		return true
	}
	if pod.Spec.Priority != nil && IsCriticalPodBasedOnPriority(*pod.Spec.Priority) {
		return true
	}
	return false
}

// Preemptable returns true if preemptor pod can preempt preemptee pod
// if preemptee is not critical or if preemptor's priority is greater than preemptee's priority
func Preemptable(preemptor, preemptee *v1.Pod) bool {
	// TODO: @shaowei. for now, tce-critical pod can't be preempted by others, but it can't preempt other pods either (for safety reasons).
	if IsTCECriticalPod(preemptee) {
		return false
	}
	if IsCriticalPod(preemptor) && !IsCriticalPod(preemptee) {
		return true
	}
	if (preemptor != nil && preemptor.Spec.Priority != nil) &&
		(preemptee != nil && preemptee.Spec.Priority != nil) {
		return *(preemptor.Spec.Priority) > *(preemptee.Spec.Priority)
	}

	return false
}

// IsTCECriticalPod returns true if the pod bears the tce-critical pod annotation key.
func IsTCECriticalPod(pod *v1.Pod) bool {
	val, ok := pod.Annotations[CriticalTCEPodAnnotationKey]
	if ok && val == "" {
		return true
	}

	return false
}

// IsIncludeNotReadyNodeDaemonPod returns true if the pod is daemon pod and
// tolerates not ready node.
func IsIncludeNotReadyNodeDaemonPod(pod *v1.Pod) bool {
	annotation := pod.GetAnnotations()
	if annotation != nil {
		value, ok := annotation[DaemonIncludeNotReadyNodeAnnotationKey]
		if ok && value == "true" {
			return true
		}
	}
	return false
}

// IsCriticalPodBasedOnPriority checks if the given pod is a critical pod based on priority resolved from pod Spec.
func IsCriticalPodBasedOnPriority(priority int32) bool {
	return priority >= scheduling.SystemCriticalPriority
}

// GetUserDemand returns real resource quantity the user demands.
func GetUserDemand(pod *v1.Pod, rn v1.ResourceName) (resource.Quantity, error) {
	var (
		demandStr, annoKey string
		quan               resource.Quantity
		ok                 bool
		err                error
	)
	switch rn {
	case v1.ResourceCPU:
		annoKey = RequestCpuUserDemandAnnotationKey
	case v1.ResourceMemory:
		annoKey = RequestMemUserDemandAnnotationKey
	default:
		return quan, fmt.Errorf("unsupported resource name %s", rn)
	}
	demandStr, ok = pod.Annotations[annoKey]
	if !ok {
		return quan, fmt.Errorf("contains no %s in annotations", annoKey)
	}
	quan, err = resource.ParseQuantity(demandStr)
	if err != nil {
		return quan, fmt.Errorf("parse user demand quantity error: %v", err)
	}
	return quan, nil
}

// GetCfsShareBurstRatio indicates the decoupling between cpu request and cfs share configuration.
func GetCfsShareBurstRatio(pod *v1.Pod, rn v1.ResourceName) (float64, error) {
	const (
		precision    int     = 3
		defaultRatio float64 = 1
	)
	var (
		ratio             float64 = 1
		ratioStr, annoKey string
		ok                bool
		err               error
	)
	switch rn {
	case v1.ResourceCPU:
		annoKey = RequestCpuCfsShareBurstRatioAnnotationKey
	case v1.ResourceMemory:
		annoKey = RequestMemCfsShareBurstRatioAnnotationKey
	default:
		return defaultRatio, fmt.Errorf("unsupported resource name %s", rn)
	}
	ratioStr, ok = pod.Annotations[annoKey]
	if !ok {
		return defaultRatio, fmt.Errorf("contains no %s in annotations", annoKey)
	}
	ratio, err = strconv.ParseFloat(ratioStr, 64)
	if err != nil {
		return defaultRatio, err
	}

	// set precision of ratio to 3.
	ratio, err = strconv.ParseFloat(fmt.Sprintf("%."+strconv.Itoa(precision)+"f", ratio), 64)
	if err != nil {
		return defaultRatio, err
	}

	return ratio, nil
}

// GetBurstRequest returns a request bursted to which equals to userDemand times cfsShareBurstRatio.
// burstRequest = userDemand * cfsShareBurstRatio
func GetBurstRequest(pod *v1.Pod, rn v1.ResourceName) (*resource.Quantity, error) {
	var (
		quan, userDemand resource.Quantity
		ratio            float64
		err              error
	)

	if userDemand, err = GetUserDemand(pod, rn); err != nil {
		return &quan, err
	}
	if ratio, err = GetCfsShareBurstRatio(pod, rn); err != nil {
		return &quan, err
	}
	if ratio <= 0 {
		return &quan, fmt.Errorf("invalid cfsShareBurstRatio: %f", ratio)
	}
	switch rn {
	case v1.ResourceCPU:
		quan.SetMilli(int64(float64(userDemand.MilliValue()) * ratio))
	case v1.ResourceMemory:
		quan.Set(int64(float64(userDemand.Value()) * ratio))
	default:
		return &quan, fmt.Errorf("unsupported resource name %s", rn)
	}
	return &quan, nil
}

// GetCpuCfsQuotaPeriod returns the period of cfs quota refilling.
func GetCpuCfsQuotaPeriod(pod *v1.Pod) (int64, error) {
	// 100000 is equivalent to 100ms
	const defaultQuotaPeriod = 100000
	var (
		quotaPeriod    int64
		quotaPeriodStr string
		ok             bool
		err            error
	)
	if quotaPeriodStr, ok = pod.Annotations[LimitCpuCfsQuotaPeriodAnnotationKey]; !ok {
		return defaultQuotaPeriod, fmt.Errorf("contains no quota period in annotations")
	}
	if quotaPeriod, err = strconv.ParseInt(quotaPeriodStr, 10, 64); err != nil {
		return defaultQuotaPeriod, err
	}
	return quotaPeriod, nil
}
