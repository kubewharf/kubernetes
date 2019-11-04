package dynamicpodspec

import (
	"fmt"
	"time"

	"k8s.io/klog"
	"k8s.io/api/core/v1"
	netutil "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

type assignPortAdmitHandler struct {
	podAnnotation string
	portRange     netutil.PortRange
	podUpdater    PodUpdater
}

func NewAssignPortHandler(podAnnotation string, portRange netutil.PortRange, podUpdater PodUpdater) *assignPortAdmitHandler {
	return &assignPortAdmitHandler{
		podAnnotation: podAnnotation,
		portRange:     portRange,
		podUpdater:    podUpdater,
	}
}

func (w *assignPortAdmitHandler) Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
	pod := attrs.Pod
	_, autoport := pod.ObjectMeta.Annotations[w.podAnnotation]
	if !autoport {
		return lifecycle.PodAdmitResult{
			Admit: true,
		}
	}

	usedPorts, lastUsedPort := getUsedPorts(attrs.OtherPods...)
	count := 0
	for i := range pod.Spec.Containers {
		for j := range pod.Spec.Containers[i].Ports {
			if pod.Spec.Containers[i].Ports[j].HostPort == 0 {
				count++
			}
		}
	}

	if count == 0 {
		return lifecycle.PodAdmitResult{
			Admit: true,
		}
	}

	port, isAssigned := getAvaPort(usedPorts, w.portRange.Base, w.portRange.Size,
		len(usedPorts), lastUsedPort, count)
	if isAssigned {
		for i := range pod.Spec.Containers {
			for j := range pod.Spec.Containers[i].Ports {
				if pod.Spec.Containers[i].Ports[j].HostPort != 0 {
					continue
				}
				pod.Spec.Containers[i].Ports[j].HostPort = int32(port)
				if pod.Spec.HostNetwork {
					pod.Spec.Containers[i].Ports[j].ContainerPort = int32(port)
				}
				envVariable := v1.EnvVar{
					Name:  fmt.Sprintf("PORT%d", j),
					Value: fmt.Sprintf("%d", pod.Spec.Containers[i].Ports[j].HostPort),
				}
				pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, envVariable)
				if j == 0 {
					pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, v1.EnvVar{
						Name:  "PORT",
						Value: fmt.Sprintf("%d", pod.Spec.Containers[i].Ports[j].HostPort),
					})
				}
				port = nextPort(port, w.portRange.Base, w.portRange.Size)
			}
		}
	} else {
		klog.V(1).Infof("no hostport can be assigned")
		return lifecycle.PodAdmitResult{
			Admit:   false,
			Reason:  "OutOfHostPort",
			Message: "Host port is exhausted.",
		}
	}
	klog.V(5).Infof("%s/%s update %d ports", pod.Namespace, pod.Name, count)
	w.podUpdater.NeedUpdate()
	return lifecycle.PodAdmitResult{
		Admit: true,
	}
}

func nextPort(port, base, max int) int {
	port++
	if port >= base+max {
		return (port-base)%max + base
	}
	return port
}

func getAvaPort(allocated map[int]bool, base, max, count, arrangeBase, portCount int) (int, bool) {
	if count >= max {
		return 0, false
	}

	if arrangeBase < base || arrangeBase >= base+max {
		arrangeBase = base
	}
Loop:
	for i := 0; i < max; i++ {
		for j := 0; j < portCount; j++ {
			if allocated[(arrangeBase-base+i+j)%max+base] == true {
				i += j
				continue Loop
			}
		}
		return (arrangeBase-base+i)%max + base, true
	}
	return 0, false
}

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

func getUsedPorts(pods ...*v1.Pod) (map[int]bool, int) {
	// TODO: Aggregate it at the NodeInfo level.
	ports := make(map[int]bool)
	lastPort := 0
	var lastPodTime time.Time
	for _, pod := range pods {
		scheduledTime := getScheduledTime(pod)
		for _, container := range pod.Spec.Containers {
			for _, podPort := range container.Ports {
				// "0" is explicitly ignored in PodFitsHostPorts,
				// which is the only function that uses this value.
				if podPort.HostPort != 0 {
					ports[int(podPort.HostPort)] = true
					if scheduledTime.After(lastPodTime) {
						lastPodTime = scheduledTime
						lastPort = int(podPort.HostPort)
					}
				}
			}
		}
	}
	return ports, lastPort
}
