package dynamicpodspec

import (
	"context"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	netutil "k8s.io/apimachinery/pkg/util/net"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

type assignPortAdmitHandler struct {
	podAnnotation string
	portRange     netutil.PortRange
	client        clientset.Interface
}

func NewAssignPortHandler(podAnnotation string, portRange netutil.PortRange, client clientset.Interface) *assignPortAdmitHandler {
	return &assignPortAdmitHandler{
		podAnnotation: podAnnotation,
		portRange:     portRange,
		client:        client,
	}
}

func (w *assignPortAdmitHandler) Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
	pod := attrs.Pod

	_, isAutoport := pod.ObjectMeta.Annotations[w.podAnnotation]
	if !isAutoport {
		return lifecycle.PodAdmitResult{
			Admit: true,
		}
	}

	numPortsToAllocate := countPortsToAllocate(pod)
	if numPortsToAllocate == 0 {
		return lifecycle.PodAdmitResult{
			Admit: true,
		}
	}

	// we use `lastUsedPort` as a heuristic to speed up or search
	allocatedPorts, lastUsedPort := getUsedPorts(attrs.OtherPods...)
	allocation := w.allocatePorts(allocatedPorts, lastUsedPort+1, numPortsToAllocate)
	if len(allocation) == 0 {
		klog.V(1).Infof("no enough hostport can be assigned for pod %v", pod.Name)
		return lifecycle.PodAdmitResult{
			Admit:   false,
			Reason:  "OutOfHostPort",
			Message: "Host port is exhausted.",
		}
	}
	// fill port info into pod related fields
	updatePodWithAllocation(allocation, pod)

	klog.V(2).Infof("%s/%s updating %d ports with allocation %v", pod.Namespace, pod.Name, numPortsToAllocate, allocation)
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
		_, updateErr := w.client.CoreV1().Pods(pod.Namespace).Update(context.Background(), pod, metav1.UpdateOptions{})
		if updateErr == nil {
			return nil
		}

		if updated, err := w.client.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{}); err == nil {
			if pod.GetUID() != updated.GetUID() {
				return fmt.Errorf("pod was deleted and then recreated, skipping pod update: oldPodUID %s, newPodUID %s", pod.GetUID(), updated.GetUID())
			}
			updatePodWithAllocation(allocation, updated)
			updated.DeepCopyInto(pod)
		}
		return updateErr
	}); err != nil {
		klog.Errorf("failed to update pod: %s/%s %v", attrs.Pod.Namespace, attrs.Pod.Name, err)
		return lifecycle.PodAdmitResult{
			Admit:   false,
			Reason:  "FailedUpdate",
			Message: fmt.Sprintf("Update pod failed. %v", err),
		}

	}

	return lifecycle.PodAdmitResult{
		Admit: true,
	}
}

func (w *assignPortAdmitHandler) allocatePorts(allocated map[int]bool, start, request int) []int {
	if request > w.portRange.Size {
		return nil
	}

	if start < w.portRange.Base || start >= w.portRange.Base+w.portRange.Size {
		start = w.portRange.Base
	}

	numAllocated := 0
	allocation := make([]int, request)

	// we start our search from `start` and loop through the port range
	for i := 0; i < w.portRange.Size; i++ {
		if numAllocated >= request {
			break
		}

		offset := (start + i - w.portRange.Base) % w.portRange.Size
		curPort := w.portRange.Base + offset

		if !allocated[curPort] {
			allocation[numAllocated] = curPort
			numAllocated++
		}
	}

	if numAllocated < request {
		return nil
	}

	return allocation
}

// countPortsToAllocate returns number of ports to be allocated for this pod
func countPortsToAllocate(pod *v1.Pod) int {
	count := 0
	for i := range pod.Spec.Containers {
		for j := range pod.Spec.Containers[i].Ports {
			if pod.Spec.Containers[i].Ports[j].HostPort == 0 {
				count++
			}
		}
	}
	return count
}

// getScheduledTime returns scheduled time of the pod
func getScheduledTime(pod *v1.Pod) time.Time {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == v1.PodScheduled {
			if condition.Status == v1.ConditionTrue {
				return condition.LastTransitionTime.Time
			}
		}
	}
	return time.Time{}
}

// getUsedPorts aggregates the already allocated ports from admited pods
func getUsedPorts(pods ...*v1.Pod) (map[int]bool, int) {
	ports := make(map[int]bool)
	lastPort := 0
	var lastPodTime time.Time

	for _, pod := range pods {
		scheduledTime := getScheduledTime(pod)

		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				if port.HostPort != 0 {
					ports[int(port.HostPort)] = true

					if scheduledTime.After(lastPodTime) {
						lastPodTime = scheduledTime
						lastPort = int(port.HostPort)
					}
				}
			}
		}

	}

	return ports, lastPort
}

// updatePodWithAllocation update pod spec with allocated ports info
// it will modify 2 places:
// 1. container port
// 2. container env
func updatePodWithAllocation(allocation []int, pod *v1.Pod) {
	allocationIdx := 0

	for i := range pod.Spec.Containers {
		containerNumAllocated := 0

		for j := range pod.Spec.Containers[i].Ports {
			if allocationIdx >= len(allocation) {
				return
			}
			if pod.Spec.Containers[i].Ports[j].HostPort != 0 {
				continue
			}

			port := allocation[allocationIdx]
			pod.Spec.Containers[i].Ports[j].HostPort = int32(port)
			if pod.Spec.HostNetwork {
				pod.Spec.Containers[i].Ports[j].ContainerPort = int32(port)
			}

			portEnv := v1.EnvVar{
				Name:  fmt.Sprintf("PORT%d", containerNumAllocated),
				Value: fmt.Sprintf("%d", port),
			}
			pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, portEnv)
			if containerNumAllocated == 0 {
				pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, v1.EnvVar{
					Name:  "PORT",
					Value: fmt.Sprintf("%d", port),
				})
			}

			containerNumAllocated++
			allocationIdx++
		}
	}
}
