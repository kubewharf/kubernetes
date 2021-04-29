package godel

import (
	api "k8s.io/kubernetes/pkg/apis/core"
)

const (
	// PodStateAnnotationKey is a pod annotation key, value is the pod state
	PodStateAnnotationKey = "godel.bytedance.com/pod-state"

	// PodResourceTypeAnnotationKey is a pod annotation key, value is the pod resource type (guaranteed or best-effort)
	PodResourceTypeAnnotationKey = "godel.bytedance.com/pod-resource-type"

	// PodLauncherAnnotationKey is a pod annotation key, value is the launcher of this pod (kubelet or node-manager)
	PodLauncherAnnotationKey = "godel.bytedance.com/pod-launcher"

	// SchedulerAnnotationKey is a pod annotation key, value is the scheduler id who is responsible for scheduling this pod
	SchedulerAnnotationKey = "godel.bytedance.com/selected-scheduler"

	// AssumedNodeAnnotationKey is a pod annotation key, value is the assumed node name chosen by one scheduler
	// the scheduler will reserve the allocated resource for the pod. TODO: should all schedulers be aware of this ?
	// TODO: figure out if we can return multiple nodes ? if so how to deal with scheduler cache ?
	AssumedNodeAnnotationKey = "godel.bytedance.com/assumed-node"

	// NominatedNodeAnnotationKey is a pod annotation key,
	// value is the node name chosen by scheduler for placing the pending pod by evicting others
	// value can be like: {node: node1, victims: pod1, pod2...}
	// the scheduler will reserve the allocated resource for the pod. TODO: should all schedulers be aware of this ?
	// TODO: figure out if we can return multiple nodes ? if so how to deal with scheduler cache ?
	NominatedNodeAnnotationKey = "godel.bytedance.com/nominated-node"

	// DefaultPriorityTypeAnnotationKey is a priority class annotation key,
	// value is the priority type which this priority class is default for
	DefaultPriorityTypeAnnotationKey = "godel.bytedance.com/default-priority-type"

	// determined by resource types and launchers, there are four priority types
	PriorityGuaranteedKublet      = "guaranteed-kubelet"
	PriorityGuaranteedNodeManager = "guaranteed-node-manager"
	PriorityBestEffortKublet      = "best-effort-kubelet"
	PriorityBestEffortNodeManager = "best-effort-node-manager"

	// default priority value for each priority type when no default priority class exists
	DefaultPriorityForGuaranteedKublet      int32 = 91
	DefaultPriorityForGuaranteedNodeManager int32 = 61
	DefaultPriorityForBestEffortKublet      int32 = 31
	DefaultPriorityForBestEffortNodeManager int32 = 1

	// pod launchers
	PodLauncherKubelet     = "kubelet"
	PodLauncherNodeManager = "node-manager"

	// pod resource types
	GuaranteedPod = "guaranteed"
	BestEffortPod = "best-effort"

	// pod states
	PodPending    = "pending"
	PodDispatched = "dispatched"
	PodAssumed    = "assumed"
)

func GetPodLauncher(pod *api.Pod) string {
	return pod.Annotations[PodLauncherAnnotationKey]
}

func SetPodLauncher(pod *api.Pod, launcher string) {
	setPodAnnotation(pod, PodLauncherAnnotationKey, launcher)
}

func GetPodResourceType(pod *api.Pod) string {
	return pod.Annotations[PodResourceTypeAnnotationKey]
}

func SetPodResourceType(pod *api.Pod, resourceType string) {
	setPodAnnotation(pod, PodResourceTypeAnnotationKey, resourceType)
}

func GetPodState(pod *api.Pod) string {
	return pod.Annotations[PodStateAnnotationKey]
}

func SetPodState(pod *api.Pod, state string) {
	setPodAnnotation(pod, PodStateAnnotationKey, state)
}

func setPodAnnotation(pod *api.Pod, key, value string) {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[key] = value
}

func GetPodPriorityType(pod *api.Pod) string {
	resourceType := GetPodResourceType(pod)
	if resourceType == "" {
		resourceType = GuaranteedPod
	}
	launcher := GetPodLauncher(pod)
	if launcher == "" {
		launcher = PodLauncherKubelet
	}

	priorityType := ""
	switch {
	case resourceType == GuaranteedPod && launcher == PodLauncherKubelet:
		priorityType = PriorityGuaranteedKublet
	case resourceType == GuaranteedPod && launcher == PodLauncherNodeManager:
		priorityType = PriorityGuaranteedNodeManager
	case resourceType == BestEffortPod && launcher == PodLauncherKubelet:
		priorityType = PriorityBestEffortKublet
	case resourceType == BestEffortPod && launcher == PodLauncherNodeManager:
		priorityType = PriorityBestEffortNodeManager
	}
	return priorityType
}

// PendingPod checks if the given pod is in pending state
func PendingPod(pod *api.Pod) bool {
	if pod.Annotations != nil &&
		(pod.Annotations[PodStateAnnotationKey] == PodPending || len(pod.Annotations[PodStateAnnotationKey]) == 0) &&
		len(pod.Annotations[SchedulerAnnotationKey]) == 0 &&
		len(pod.Annotations[AssumedNodeAnnotationKey]) == 0 &&
		len(pod.Annotations[NominatedNodeAnnotationKey]) == 0 &&
		len(pod.Spec.NodeName) == 0 {
		return true
	}
	return false
}

// DispatchedPod checks if the given pod is in dispatched state
func DispatchedPod(pod *api.Pod) bool {
	if pod.Annotations != nil &&
		pod.Annotations[PodStateAnnotationKey] == PodDispatched &&
		len(pod.Annotations[SchedulerAnnotationKey]) != 0 &&
		len(pod.Annotations[AssumedNodeAnnotationKey]) == 0 &&
		len(pod.Annotations[NominatedNodeAnnotationKey]) == 0 &&
		len(pod.Spec.NodeName) == 0 {
		return true
	}
	return false
}

// assumedOrNominatedNodeIsSet checks if the AssumedNodeAnnotationKey or NominatedNodeAnnotationKey is set
func assumedOrNominatedNodeIsSet(pod *api.Pod) bool {
	if pod.Annotations != nil {
		if len(pod.Annotations[AssumedNodeAnnotationKey]) == 0 && len(pod.Annotations[NominatedNodeAnnotationKey]) != 0 {
			return true
		}
		if len(pod.Annotations[AssumedNodeAnnotationKey]) != 0 && len(pod.Annotations[NominatedNodeAnnotationKey]) == 0 {
			return true
		}
	}
	return false
}

// AssumedPod checks if the given pod is in assumed state
func AssumedPod(pod *api.Pod) bool {
	if pod.Annotations != nil &&
		pod.Annotations[PodStateAnnotationKey] == PodAssumed &&
		len(pod.Annotations[SchedulerAnnotationKey]) != 0 &&
		assumedOrNominatedNodeIsSet(pod) &&
		len(pod.Spec.NodeName) == 0 {
		return true
	}
	return false
}

// BoundPod checks if the given pod is bound
func BoundPod(pod *api.Pod) bool {
	return len(pod.Spec.NodeName) != 0
}

// AbnormalPodState checks if the given pod is in abnormal state
func AbnormalPodState(pod *api.Pod) bool {
	if BoundPod(pod) {
		return false
	}

	switch pod.Annotations[PodStateAnnotationKey] {
	case "", PodPending:
		if !PendingPod(pod) {
			return true
		} else {
			return false
		}
	case PodDispatched:
		if !DispatchedPod(pod) {
			return true
		} else {
			return false
		}
	case PodAssumed:
		if !AssumedPod(pod) {
			return true
		} else {
			return false
		}
	default:
		return true
	}
}
