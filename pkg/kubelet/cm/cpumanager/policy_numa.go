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

package cpumanager

import (
	"k8s.io/klog"
	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/state"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
)

// PolicyNuma is the name of the numa policy
const PolicyNuma policyName = "numa"

type numaPolicy struct {
	staticPolicy
}

// Ensure numaPolicy implements Policy interface
var _ Policy = &numaPolicy{}

func NewNumaPolicy(topology *topology.CPUTopology, numReservedCPUs int) Policy {
	reserved := newCPUReserved(topology, numReservedCPUs)
	return &numaPolicy{
		staticPolicy: staticPolicy{topology, reserved},
	}
}

func (p *numaPolicy) Name() string {
	return string(PolicyNuma)
}

func (p *numaPolicy) Start(s state.State) {
	if err := p.validateState(s); err != nil {
		klog.Errorf("[cpumanager] numa policy invalid state: %s\n", err.Error())
		panic("[cpumanager] - please drain node and remove policy state file")
	}
}

func (p *numaPolicy) assignableCPUsAndMemorys(s state.State) (cpuset.CPUSet, cpuset.CPUSet) {
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

	var selectedSocketId, maxSocketCPUs int = 0, 0
	for socketId, builder := range socketBuilder {
		if builder.Result().Size() > maxSocketCPUs {
			maxSocketCPUs = builder.Result().Size()
			selectedSocketId = socketId
		}
	}
	return socketBuilder[selectedSocketId].Result(), cpuset.NewCPUSet(selectedSocketId)

}

func (p *numaPolicy) allocateCPUsAndMemorys(s state.State, numCPUs int) (cpuset.CPUSet, cpuset.CPUSet, error) {
	klog.Infof("[cpumanager] allocateCPUsAndMemorys: (numCPUs: %d)", numCPUs)
	assignableCPUs, mems := p.assignableCPUsAndMemorys(s)
	cpus, err := takeByTopology(p.topology, assignableCPUs, numCPUs)
	if err != nil {
		return cpuset.NewCPUSet(), cpuset.NewCPUSet(), err
	}
	// Remove allocated CPUs from the shared CPUSet.
	s.SetDefaultCPUSet(s.GetDefaultCPUSet().Difference(cpus))

	klog.Infof("[cpumanager] allocateCPUs: returning \"%v\"", cpus)
	return cpus, mems, nil
}

func (p *numaPolicy) AddContainer(s state.State, pod *v1.Pod, container *v1.Container, containerID string) error {
	if numCPUs := guaranteedCPUs(pod, container); numCPUs != 0 {
		klog.Infof("[cpumanager] numa policy: AddContainer (pod: %s, container: %s, container id: %s)", pod.Name, container.Name, containerID)
		// container belongs in an exclusively allocated pool

		if _, ok := s.GetCPUSet(containerID); ok {
			klog.Infof("[cpumanager] numa policy: container already present in state, skipping (container: %s, container id: %s)", container.Name, containerID)
			return nil
		}

		cpu, mem, err := p.allocateCPUsAndMemorys(s, numCPUs)
		if err != nil {
			klog.Errorf("[cpumanager] unable to allocate %d CPUs (container id: %s, error: %v)", numCPUs, containerID, err)
			return err
		}
		s.SetCPUSet(containerID, cpu)
		s.SetCPUSetMemory(containerID, mem)
	}
	// container belongs in the shared pool (nothing to do; use default cpuset)
	return nil

}

func (p *numaPolicy) RemoveContainer(s state.State, containerID string) error {
	klog.Infof("[cpumanager] numa policy: RemoveContainer (container id: %s)", containerID)
	if toRelease, ok := s.GetCPUSet(containerID); ok {
		s.Delete(containerID)
		// Mutate the shared pool, adding released cpus.
		s.SetDefaultCPUSet(s.GetDefaultCPUSet().Union(toRelease))
	}
	return nil
}
