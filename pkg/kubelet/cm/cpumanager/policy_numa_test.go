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
	"fmt"
	"reflect"
	"testing"

	"k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
)

type numaPolicyTest struct {
	description     string
	topo            *topology.CPUTopology
	numReservedCPUs int
	containerID     string
	stAssignments   map[string]cpuset.CPUSet
	memAssignments  map[string]cpuset.CPUSet
	stDefaultCPUSet cpuset.CPUSet
	pod             *v1.Pod
	expErr          error
	expCPUAlloc     bool
	expCSet         cpuset.CPUSet
	expMSet         cpuset.CPUSet
}

func TestNumaPolicyName(t *testing.T) {
	policy := NewNumaPolicy(topoSingleSocketHT, 1)

	policyName := policy.Name()
	if policyName != "numa" {
		t.Errorf("NumaPolicy Name() error. expected: numa, returned: %v",
			policyName)
	}
}

func TestNumaPolicyStart(t *testing.T) {
	policy := NewNumaPolicy(topoSingleSocketHT, 1).(*numaPolicy)

	st := &mockState{
		assignments:    map[string]cpuset.CPUSet{},
		memAssignments: map[string]cpuset.CPUSet{},
		defaultCPUSet:  cpuset.NewCPUSet(),
	}

	policy.Start(st)
	for cpuid := 1; cpuid < policy.topology.NumCPUs; cpuid++ {
		if !st.defaultCPUSet.Contains(cpuid) {
			t.Errorf("NumaPolicy Start() error. expected cpuid %d to be present in defaultCPUSet", cpuid)
		}
	}
}

func TestNumaPolicyAdd(t *testing.T) {
	largeTopoBuilder := cpuset.NewBuilder()
	largeTopoSock0Builder := cpuset.NewBuilder()
	largeTopoSock1Builder := cpuset.NewBuilder()
	largeTopo := *topoQuadSocketFourWayHT
	for cpuid, val := range largeTopo.CPUDetails {
		largeTopoBuilder.Add(cpuid)
		if val.SocketID == 0 {
			largeTopoSock0Builder.Add(cpuid)
		} else if val.SocketID == 1 {
			largeTopoSock1Builder.Add(cpuid)
		}
	}
	largeTopoCPUSet := largeTopoBuilder.Result()
	largeTopoSock0CPUSet := largeTopoSock0Builder.Result()

	testCases := []numaPolicyTest{
		{
			description:     "GuPodSingleCore, SingleSocketHT, ExpectError",
			topo:            topoSingleSocketHT,
			numReservedCPUs: 1,
			containerID:     "fakeID2",
			stAssignments:   map[string]cpuset.CPUSet{},
			memAssignments:  map[string]cpuset.CPUSet{},
			stDefaultCPUSet: cpuset.NewCPUSet(0, 1, 2, 3, 4, 5, 6, 7),
			pod:             makePod("8000m", "8000m"),
			expErr:          fmt.Errorf("not enough cpus available to satisfy request"),
			expCPUAlloc:     false,
			expCSet:         cpuset.NewCPUSet(),
			expMSet:         cpuset.NewCPUSet(),
		},
		{
			description:     "GuPodMultipleCores, DualSocketHT, ExpectAllocOneSocket",
			topo:            topoDualSocketHT,
			numReservedCPUs: 1,
			containerID:     "fakeID3",
			stAssignments: map[string]cpuset.CPUSet{
				"fakeID100": cpuset.NewCPUSet(2),
			},
			memAssignments:  map[string]cpuset.CPUSet{},
			stDefaultCPUSet: cpuset.NewCPUSet(0, 1, 3, 4, 5, 6, 7, 8, 9, 10, 11),
			pod:             makePod("6000m", "6000m"),
			expErr:          nil,
			expCPUAlloc:     true,
			expCSet:         cpuset.NewCPUSet(1, 3, 5, 7, 9, 11),
			expMSet:         cpuset.NewCPUSet(1),
		},
		{
			description:     "GuPodMultipleCores, DualSocketHT, ExpectAllocOneSocket#2",
			topo:            topoDualSocketHT,
			numReservedCPUs: 1,
			containerID:     "fakeID3",
			stAssignments: map[string]cpuset.CPUSet{
				"fakeID100": cpuset.NewCPUSet(1, 3, 5, 7, 9),
			},
			memAssignments: map[string]cpuset.CPUSet{
				"fakeID100": cpuset.NewCPUSet(1),
			},
			stDefaultCPUSet: cpuset.NewCPUSet(0, 2, 4, 6, 8, 10),
			pod:             makePod("5000m", "5000m"),
			expErr:          nil,
			expCPUAlloc:     true,
			expCSet:         cpuset.NewCPUSet(2, 4, 6, 8, 10),
			expMSet:         cpuset.NewCPUSet(0),
		},
		{
			description:     "GuPodMultipleCores, DualSocketHT, ExpectErr",
			topo:            topoDualSocketHT,
			numReservedCPUs: 1,
			containerID:     "fakeID3",
			stAssignments: map[string]cpuset.CPUSet{
				"fakeID100": cpuset.NewCPUSet(2),
			},
			memAssignments: map[string]cpuset.CPUSet{},
			// no single socket has 6 cpus
			stDefaultCPUSet: cpuset.NewCPUSet(0, 1, 3, 4, 5, 6, 7, 8, 9, 10),
			pod:             makePod("6000m", "6000m"),
			expErr:          fmt.Errorf("not enough cpus available to satisfy request"),
			expCPUAlloc:     false,
			expCSet:         cpuset.NewCPUSet(),
			expMSet:         cpuset.NewCPUSet(),
		},
		{
			// All the CPUs from Socket 0 are available. Some CPUs from each
			// Socket have been already assigned.
			// Expect all CPUs from Socket 0.
			description: "GuPodMultipleCores, topoQuadSocketFourWayHT, ExpectAllocSock0",
			topo:        topoQuadSocketFourWayHT,
			containerID: "fakeID5",
			stAssignments: map[string]cpuset.CPUSet{
				"fakeID100": cpuset.NewCPUSet(3, 11, 4, 5, 6, 7),
			},
			memAssignments:  map[string]cpuset.CPUSet{},
			stDefaultCPUSet: largeTopoCPUSet.Difference(cpuset.NewCPUSet(3, 11, 4, 5, 6, 7)),
			pod:             makePod("72000m", "72000m"),
			expErr:          nil,
			expCPUAlloc:     true,
			expCSet:         largeTopoSock0CPUSet,
			expMSet:         cpuset.NewCPUSet(0),
		},
	}

	for _, testCase := range testCases {
		policy := NewNumaPolicy(testCase.topo, testCase.numReservedCPUs)

		st := &mockState{
			assignments:    testCase.stAssignments,
			defaultCPUSet:  testCase.stDefaultCPUSet,
			memAssignments: testCase.memAssignments,
		}

		container := &testCase.pod.Spec.Containers[0]
		err := policy.AddContainer(st, testCase.pod, container, testCase.containerID)
		if !reflect.DeepEqual(err, testCase.expErr) {
			t.Errorf("NumaPolicy AddContainer() error (%v). expected add error: %v but got: %v",
				testCase.description, testCase.expErr, err)
		}

		if testCase.expCPUAlloc {
			cset, found := st.assignments[testCase.containerID]
			if !found {
				t.Errorf("NumaPolicy AddContainer() error (%v). expected container id %v to be present in assignments %v",
					testCase.description, testCase.containerID, st.assignments)
			}

			if !reflect.DeepEqual(cset, testCase.expCSet) {
				t.Errorf("NumaPolicy AddContainer() error (%v). expected cpuset %v but got %v",
					testCase.description, testCase.expCSet, cset)
			}

			if !cset.Intersection(st.defaultCPUSet).IsEmpty() {
				t.Errorf("NumaPolicy AddContainer() error (%v). expected cpuset %v to be disoint from the shared cpuset %v",
					testCase.description, cset, st.defaultCPUSet)
			}

			mems, found := st.memAssignments[testCase.containerID]
			if !found {
				t.Errorf("NumaPolicy AddContainer() error (%v). expected container id %v to be present in mem assignments %v",
					testCase.description, testCase.containerID, st.memAssignments)
			}

			if !mems.Equals(testCase.expMSet) {
				t.Errorf("NumaPolicy AddContainer() error (%v). expected mem assignments %v", testCase.description, st.memAssignments)
			}
		}

		if !testCase.expCPUAlloc {
			_, found := st.assignments[testCase.containerID]
			if found {
				t.Errorf("NumaPolicy AddContainer() error (%v). Did not expect container id %v to be present in assignments %v",
					testCase.description, testCase.containerID, st.assignments)
			}
		}
	}
}

func TestNumaPolicyRemove(t *testing.T) {
	testCases := []numaPolicyTest{
		{
			description: "SingleSocketHT, DeAllocOneContainer",
			topo:        topoSingleSocketHT,
			containerID: "fakeID1",
			stAssignments: map[string]cpuset.CPUSet{
				"fakeID1": cpuset.NewCPUSet(1, 2, 3),
			},
			memAssignments: map[string]cpuset.CPUSet{
				"fakeID1": cpuset.NewCPUSet(0),
			},
			stDefaultCPUSet: cpuset.NewCPUSet(4, 5, 6, 7),
			expCSet:         cpuset.NewCPUSet(1, 2, 3, 4, 5, 6, 7),
			expMSet:         cpuset.NewCPUSet(),
		},
	}

	for _, testCase := range testCases {
		policy := NewNumaPolicy(testCase.topo, testCase.numReservedCPUs)

		st := &mockState{
			assignments:    testCase.stAssignments,
			memAssignments: testCase.memAssignments,
			defaultCPUSet:  testCase.stDefaultCPUSet,
		}

		policy.RemoveContainer(st, testCase.containerID)

		if !reflect.DeepEqual(st.defaultCPUSet, testCase.expCSet) {
			t.Errorf("NumaPolicy RemoveContainer() error (%v). expected default cpuset %v but got %v",
				testCase.description, testCase.expCSet, st.defaultCPUSet)
		}

		if _, found := st.assignments[testCase.containerID]; found {
			t.Errorf("NumaPolicy RemoveContainer() error (%v). expected containerID %v not be in assignments %v",
				testCase.description, testCase.containerID, st.assignments)
		}
	}
}
