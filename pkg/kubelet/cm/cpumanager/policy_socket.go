package cpumanager

import (
	"fmt"
	"sort"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/state"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
)

const PolicySocket policyName = "socket"

type socketPolicy struct {
	staticPolicy
}

// Ensure socketPolicy implements Policy interface
var _ Policy = &socketPolicy{}

func NewSocketPolicy(topology *topology.CPUTopology, numReservedCPUs int) Policy {
	reserved := newCPUReserved(topology, numReservedCPUs)
	return &socketPolicy{staticPolicy{topology: topology, reserved: reserved}}
}

func (p *socketPolicy) Start(s state.State) {
	if err := p.validateState(s); err != nil {
		klog.Errorf("[cpumanager] socket policy invalid state: %s\n", err.Error())
		panic("[cpumanager] - please drain node and remove policy state file")
	}
}

func (p *socketPolicy) Name() string {
	return string(PolicySocket)
}

func (p *socketPolicy) assignableSocketCPUs(s state.State, numSockets int) (cpuset.CPUSet, error) {
	freeCPUs := s.GetDefaultCPUSet().Difference(p.reserved)

	socketBuilder := map[int]cpuset.Builder{}
	for _, cpuid := range freeCPUs.ToSlice() {
		socketId := p.topology.CPUDetails[cpuid].SocketID
		builder, found := socketBuilder[socketId]
		if !found {
			builder = cpuset.NewBuilder()
			socketBuilder[socketId] = builder
		}
		builder.Add(cpuid)
	}

	//Find the socket containing the maximum cpus
	var res cpuset.CPUSet
	// If the free socket num is less than the requested socket num
	if len(socketBuilder) < numSockets {
		return res, fmt.Errorf("the free socket num(%d) is less than the requested socket num(%d)", len(socketBuilder), numSockets)
	}
	var sizeMap = make(map[int][]int)
	var sizeArr []int
	for socketId, builder := range socketBuilder {
		size := builder.Result().Size()
		sizeMap[size] = append(sizeMap[size], socketId)
	}
	for size, _ := range sizeMap {
		sizeArr = append(sizeArr, size)
	}
	sort.Ints(sizeArr)
	var count = 0
	for i := len(sizeArr) - 1; i >= 0; i-- {
		socketIdArr := sizeMap[sizeArr[i]]
		for _, socketId := range socketIdArr {
			if count < numSockets {
				res = res.Union(socketBuilder[socketId].Result())
				count++
			} else {
				return res, nil
			}
		}
	}

	return res, nil
}

func (p *socketPolicy) allocateSocketCPUs(s state.State, numSockets int) (cpuset.CPUSet, error) {
	klog.Infof("[cpumanager] allocateSockets: (numSockets: %d)", numSockets)
	assignableCPUs, err := p.assignableSocketCPUs(s, numSockets)
	if err != nil {
		return assignableCPUs, err
	}

	// Remove allocated CPUs from the shared CPUSet.
	s.SetDefaultCPUSet(s.GetDefaultCPUSet().Difference(assignableCPUs))

	klog.Infof("[cpumanager] allocateSocketCPUs: returning \"%v\"", assignableCPUs)
	return assignableCPUs, nil
}

func (p *socketPolicy) AddContainer(s state.State, pod *v1.Pod, container *v1.Container, containerID string) error {
	if numSockets := requestSockets(container); numSockets != 0 {
		klog.Infof("[cpumanager] socket policy: AddContainer (pod: %s, container: %s, container id: %s)", pod.Name, container.Name, containerID)
		if _, ok := s.GetCPUSet(containerID); ok {
			klog.Infof("[cpumanager] socket policy: container already present in state, skipping (container: %s, container id: %s)", container.Name, containerID)
			return nil
		}
		cpu, err := p.allocateSocketCPUs(s, numSockets)
		if err != nil {
			return fmt.Errorf("Container: %s(%s): %s", container.Name, containerID, string(err.Error()))
		}
		s.SetCPUSet(containerID, cpu)
	}

	return nil
}

func (p *socketPolicy) RemoveContainer(s state.State, containerID string) error {
	klog.Infof("[cpumanager] socket policy: RemoveContainer (container id: %s)", containerID)
	if toRelease, ok := s.GetCPUSet(containerID); ok {
		s.Delete(containerID)
		// Mutate the shared pool, adding released cpus.
		s.SetDefaultCPUSet(s.GetDefaultCPUSet().Union(toRelease))
	}
	return nil
}

func requestSockets(container *v1.Container) int {
	socketQuantity := container.Resources.Requests[v1.ResourceBytedanceSocket]
	return int(socketQuantity.Value())
}
