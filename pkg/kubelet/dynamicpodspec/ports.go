package dynamicpodspec

import (
	"fmt"
	"math/rand"
	"time"

	v1 "k8s.io/api/core/v1"
	netutil "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

const (
	autoPortRandom     = "random"
	autoPortSequential = "sequential"
	networkTCP         = "tcp"
)

type assignPortAdmitHandler struct {
	podAnnotation string
	portRange     netutil.PortRange
	podUpdater    PodUpdater
	rander        *rand.Rand
}

func NewAssignPortHandler(podAnnotation string, portRange netutil.PortRange, podUpdater PodUpdater) *assignPortAdmitHandler {
	return &assignPortAdmitHandler{
		podAnnotation: podAnnotation,
		portRange:     portRange,
		podUpdater:    podUpdater,
		rander:        rand.New(rand.NewSource(time.Now().Unix())),
	}
}

func (w *assignPortAdmitHandler) Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
	pod := attrs.Pod
	autoPortType, exists := pod.ObjectMeta.Annotations[w.podAnnotation]
	if !exists {
		return lifecycle.PodAdmitResult{
			Admit: true,
		}
	}
	var r *rand.Rand
	switch autoPortType {
	case autoPortSequential:
		break
	case autoPortRandom:
		fallthrough
	default:
		r = w.rander
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

	availablePorts, canAssigned := getAvailablePorts(usedPorts, w.portRange.Base, w.portRange.Size, lastUsedPort, count, r)
	if !canAssigned {
		klog.V(1).Infof("no hostport can be assigned")
		return lifecycle.PodAdmitResult{
			Admit:   false,
			Reason:  "OutOfHostPort",
			Message: "Host port is exhausted.",
		}
	}
	portIndex := 0
	for i := range pod.Spec.Containers {
		for j := range pod.Spec.Containers[i].Ports {
			if pod.Spec.Containers[i].Ports[j].HostPort != 0 {
				continue
			}
			port := availablePorts[portIndex]
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
			portIndex++
		}
	}

	klog.V(5).Infof("%s/%s update %d ports", pod.Namespace, pod.Name, count)
	w.podUpdater.NeedUpdate()
	return lifecycle.PodAdmitResult{
		Admit: true,
	}
}

func getAvailablePorts(allocated map[int]bool, base, max, arrangeBase, portCount int, rander *rand.Rand) ([]int, bool) {
	usedCount := len(allocated)
	if usedCount >= max {
		// all ports has been assigned
		return nil, false
	}

	if allocated == nil {
		allocated = map[int]bool{}
	}

	if arrangeBase < base || arrangeBase >= base+max {
		arrangeBase = base
	}
	availablePortLength := max - len(allocated)
	if availablePortLength < portCount {
		// no enough ports
		return nil, false
	}
	allPorts := make([]int, availablePortLength)
	var offset = 0
	var startIndex = 0
	var findFirstPort = false
	var result []int
	for i := 0; i < availablePortLength; {
		port := base + i + offset
		if used := allocated[port]; used {
			offset += 1
			continue
		}
		if !findFirstPort && port >= arrangeBase {
			startIndex = i
			findFirstPort = true
		}
		allPorts[i] = port
		i++
	}

	if rander != nil {
		rander.Shuffle(availablePortLength, func(i, j int) {
			allPorts[i], allPorts[j] = allPorts[j], allPorts[i]
		})
	}
	for i := 0; i < availablePortLength; i++ {
		index := (i + startIndex) % availablePortLength
		port := allPorts[index]
		if !isPortAvailable(networkTCP, port) {
			klog.V(4).Infof("cannot used %d, skip it", port)
			continue
		}
		result = append(result, port)
		if len(result) == portCount {
			return result, true
		}
	}
	return result, false
}

func getUsedPorts(pods ...*v1.Pod) (map[int]bool, int) {
	// TODO: Aggregate it at the NodeInfo level.
	ports := make(map[int]bool)
	lastPort := 0
	var lastPodTime time.Time
	for _, pod := range pods {
		scheduledTime := getScheduledTime(pod)
		podPorts := getPodPorts(pod)
		for podPort := range podPorts {
			if podPort == 0 {
				// "0" is explicitly ignored in PodFitsHostPorts,
				// which is the only function that uses this value.
				continue
			}
			ports[podPort] = true
			if scheduledTime.After(lastPodTime) {
				lastPodTime = scheduledTime
				if podPort <= lastPort {
					continue
				}
				lastPort = podPort
			}
		}
	}
	return ports, lastPort
}
