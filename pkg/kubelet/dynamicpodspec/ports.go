package dynamicpodspec

import (
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	netutil "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/klog"
	utilpod "k8s.io/kubernetes/pkg/api/pod"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

const (
	autoPortRandom     = "random"
	autoPortSequential = "sequential"
	networkTCP         = "tcp"
)

type PortStatus struct {
	mu           sync.RWMutex
	lastUsedPort int
	ports        map[int]bool
}

func NewPortStatus(portRange netutil.PortRange) *PortStatus {
	portStatus := &PortStatus{
		mu:           sync.RWMutex{},
		ports:        make(map[int]bool),
		lastUsedPort: -1,
	}

	for i := 0; i < portRange.Size; i++ {
		portStatus.ports[portRange.Base+i] = false
	}

	return portStatus
}

func (ps *PortStatus) Sync(pods []*v1.Pod) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.sync(pods)
}

func (ps *PortStatus) sync(pods []*v1.Pod) {
	usedPorts, lastUsedPort := getUsedPorts(pods...)
	if lastUsedPort > 0 {
		ps.lastUsedPort = lastUsedPort
	}

	for port := range usedPorts {
		ps.ports[port] = true
	}

	for port, used := range ps.ports {
		if !used {
			continue
		}
		if val, ok := usedPorts[port]; !val || !ok {
			ps.ports[port] = false
		}
	}
}

func (ps *PortStatus) get() (map[int]bool, int) {
	m := make(map[int]bool)
	for k, v := range ps.ports {
		if v {
			m[k] = v
		}
	}
	return m, ps.lastUsedPort
}

func (ps *PortStatus) Get() (map[int]bool, int) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.get()
}

func (ps *PortStatus) assign(ports []int) {
	for i := range ports {
		ps.ports[ports[i]] = true
	}
	return
}

func (ps *PortStatus) Assign(ports []int) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.assign(ports)
}

type assignPortAdmitHandler struct {
	podAnnotation string
	portRange     netutil.PortRange
	podUpdater    PodUpdater
	rander        *rand.Rand
	portStatus    *PortStatus
}

func NewAssignPortHandler(podAnnotation string, portRange netutil.PortRange, podUpdater PodUpdater) *assignPortAdmitHandler {
	return &assignPortAdmitHandler{
		podAnnotation: podAnnotation,
		portRange:     portRange,
		podUpdater:    podUpdater,
		rander:        rand.New(rand.NewSource(time.Now().Unix())),
		portStatus:    NewPortStatus(portRange),
	}
}

func (w *assignPortAdmitHandler) Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
	pod := attrs.Pod
	w.portStatus.Assign(getPodUsedPorts(pod))
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

	overridePortsSet := utilpod.GetOverridePorts(pod)
	count := 0
	for i := range pod.Spec.Containers {
		for j := range pod.Spec.Containers[i].Ports {
			klog.V(1).Infof("admit pod %s, override: %q, hostPort: %s", pod.Name, overridePortsSet, string(pod.Spec.Containers[i].Ports[j].HostPort))
			if overridePortsSet.Has(fmt.Sprint(pod.Spec.Containers[i].Ports[j].HostPort)) {
				count++
			}
		}
	}
	klog.V(1).Infof("admit pod %s, override: %q, count: %d", pod.Name, overridePortsSet, count)

	if count == 0 {
		return lifecycle.PodAdmitResult{
			Admit: true,
		}
	}

	w.portStatus.mu.Lock()

	w.portStatus.sync(attrs.OtherPods)
	usedPorts, lastUsedPort := w.portStatus.get()

	availablePorts, canAssigned := getAvailablePorts(usedPorts, w.portRange.Base, w.portRange.Size, lastUsedPort, count, r)
	klog.V(1).Infof("admit pod %s, override: %q, available: %q, used: %#v, lastUsed: %#v", pod.Name, overridePortsSet, availablePorts, usedPorts, lastUsedPort)
	if !canAssigned {
		w.portStatus.mu.Unlock()
		klog.V(1).Infof("no hostport can be assigned")
		return lifecycle.PodAdmitResult{
			Admit:   false,
			Reason:  "OutOfHostPort",
			Message: "Host port is exhausted.",
		}
	}

	w.portStatus.assign(availablePorts)
	w.portStatus.mu.Unlock()

	portIndex := 0
	klog.V(1).Infof("admit pod %s, override: %q, available: %q", pod.Name, overridePortsSet, availablePorts)
	for i := range pod.Spec.Containers {
		for j := range pod.Spec.Containers[i].Ports {
			if !overridePortsSet.Has(fmt.Sprint(pod.Spec.Containers[i].Ports[j].HostPort)) {
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

/*
Use listen to test the local port is available or not.
*/
func isPortAvailable(network string, port int) bool {
	conn, err := net.Listen(network, ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
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
		usedPorts := getPodUsedPorts(pod)
		for i := range usedPorts {
			ports[usedPorts[i]] = true
		}
		if scheduledTime.After(lastPodTime) && len(usedPorts) > 0 {
			lastPort = int(usedPorts[len(usedPorts)-1])
		}
	}
	return ports, lastPort
}

func getPodUsedPorts(pod *v1.Pod) []int {
	ports := []int{}
	overridePorts := utilpod.GetOverridePorts(pod)
	for _, container := range pod.Spec.Containers {
		for _, podPort := range container.Ports {
			// "0" is explicitly ignored in PodFitsHostPorts,
			// which is the only function that uses this value.
			if !overridePorts.Has(string(podPort.HostPort)) {
				ports = append(ports, int(podPort.HostPort))
			}
		}
	}
	return ports
}
