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

package topology

import (
	"errors"
	"fmt"
	"io/ioutil"
	"strings"

	cadvisorapi "github.com/google/cadvisor/info/v1"
	"k8s.io/klog"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"k8s.io/kubernetes/pkg/kubelet/nadvisor"
)

// NUMANodeInfo is a map from NUMANode ID to a list of CPU IDs associated with
// that NUMANode.
type NUMANodeInfo map[int]cpuset.CPUSet

// CPUDetails is a map from CPU ID to Core ID, Socket ID, and NUMA ID.
type CPUDetails map[int]CPUInfo

// CPUTopology contains details of node cpu, where :
// CPU  - logical CPU, cadvisor - thread
// Core - physical CPU, cadvisor - Core
// Socket - socket, cadvisor - Node
type CPUTopology struct {
	NumCPUs    int
	NumCores   int
	NumSockets int
	CPUDetails CPUDetails
}

// CPUsPerCore returns the number of logical CPUs are associated with
// each core.
func (topo *CPUTopology) CPUsPerCore() int {
	if topo.NumCores == 0 {
		return 0
	}
	return topo.NumCPUs / topo.NumCores
}

// CPUsPerSocket returns the number of logical CPUs are associated with
// each socket.
func (topo *CPUTopology) CPUsPerSocket() int {
	if topo.NumSockets == 0 {
		return 0
	}
	return topo.NumCPUs / topo.NumSockets
}

func (topo *CPUTopology) CheckValid() error {
	if topo.CPUDetails == nil || len(topo.CPUDetails) == 0 {
		return errors.New("cpu topology cpu detail is nil or empty")
	}
	if topo.CPUDetails.Sockets().Size() == 0 {
		return errors.New("cpu topology cpu detail socket is zero")
	}
	if topo.CPUDetails.NUMANodes().Size()%topo.CPUDetails.Sockets().Size() != 0 {
		return fmt.Errorf("cpu topology cpu detail numa size %d can't divide by socket size %d", topo.CPUDetails.NUMANodes().Size(), topo.CPUDetails.Sockets().Size())
	}

	avgNumaSizeInSocket := topo.CPUDetails.NUMANodes().Size() / topo.CPUDetails.Sockets().Size()
	// key is socket id, value is numa in socket
	socketInfo := make(map[int]sets.Int)
	for _, cpuInfo := range topo.CPUDetails {
		// calculate numa number in socket
		if _, ok := socketInfo[cpuInfo.SocketID]; !ok {
			socketInfo[cpuInfo.SocketID] = sets.NewInt()
		}
		socketInfo[cpuInfo.SocketID].Insert(cpuInfo.NUMANodeID)
	}
	for socketId, numaIdSet := range socketInfo {
		if len(numaIdSet) != avgNumaSizeInSocket {
			return fmt.Errorf("cpu topology cpu detail socket id %d numa size is %d, avg numa size is %d",
				socketId, numaIdSet, avgNumaSizeInSocket)
		}
	}
	return nil
}

// CPUInfo contains the socket and core IDs associated with a CPU.
type CPUInfo struct {
	NUMANodeID int
	SocketID   int
	CoreID     int
}

// KeepOnly returns a new CPUDetails object with only the supplied cpus.
func (d CPUDetails) KeepOnly(cpus cpuset.CPUSet) CPUDetails {
	result := CPUDetails{}
	for cpu, info := range d {
		if cpus.Contains(cpu) {
			result[cpu] = info
		}
	}
	return result
}

// NUMANodes returns all of the NUMANode IDs associated with the CPUs in this
// CPUDetails.
func (d CPUDetails) NUMANodes() cpuset.CPUSet {
	b := cpuset.NewBuilder()
	for _, info := range d {
		b.Add(info.NUMANodeID)
	}
	return b.Result()
}

// NUMANodesInSockets returns all of the logical NUMANode IDs associated with
// the given socket IDs in this CPUDetails.
func (d CPUDetails) NUMANodesInSockets(ids ...int) cpuset.CPUSet {
	b := cpuset.NewBuilder()
	for _, id := range ids {
		for _, info := range d {
			if info.SocketID == id {
				b.Add(info.NUMANodeID)
			}
		}
	}
	return b.Result()
}

// Sockets returns all of the socket IDs associated with the CPUs in this
// CPUDetails.
func (d CPUDetails) Sockets() cpuset.CPUSet {
	b := cpuset.NewBuilder()
	for _, info := range d {
		b.Add(info.SocketID)
	}
	return b.Result()
}

// CPUsInSockets returns all of the logical CPU IDs associated with the given
// socket IDs in this CPUDetails.
func (d CPUDetails) CPUsInSockets(ids ...int) cpuset.CPUSet {
	b := cpuset.NewBuilder()
	for _, id := range ids {
		for cpu, info := range d {
			if info.SocketID == id {
				b.Add(cpu)
			}
		}
	}
	return b.Result()
}

// SocketsInNUMANodes returns all of the logical Socket IDs associated with the
// given NUMANode IDs in this CPUDetails.
func (d CPUDetails) SocketsInNUMANodes(ids ...int) cpuset.CPUSet {
	b := cpuset.NewBuilder()
	for _, id := range ids {
		for _, info := range d {
			if info.NUMANodeID == id {
				b.Add(info.SocketID)
			}
		}
	}
	return b.Result()
}

// Cores returns all of the core IDs associated with the CPUs in this
// CPUDetails.
func (d CPUDetails) Cores() cpuset.CPUSet {
	b := cpuset.NewBuilder()
	for _, info := range d {
		b.Add(info.CoreID)
	}
	return b.Result()
}

// CoresInNUMANodes returns all of the core IDs associated with the given
// NUMANode IDs in this CPUDetails.
func (d CPUDetails) CoresInNUMANodes(ids ...int) cpuset.CPUSet {
	b := cpuset.NewBuilder()
	for _, id := range ids {
		for _, info := range d {
			if info.NUMANodeID == id {
				b.Add(info.CoreID)
			}
		}
	}
	return b.Result()
}

// CoresInSockets returns all of the core IDs associated with the given socket
// IDs in this CPUDetails.
func (d CPUDetails) CoresInSockets(ids ...int) cpuset.CPUSet {
	b := cpuset.NewBuilder()
	for _, id := range ids {
		for _, info := range d {
			if info.SocketID == id {
				b.Add(info.CoreID)
			}
		}
	}
	return b.Result()
}

// CPUs returns all of the logical CPU IDs in this CPUDetails.
func (d CPUDetails) CPUs() cpuset.CPUSet {
	b := cpuset.NewBuilder()
	for cpuID := range d {
		b.Add(cpuID)
	}
	return b.Result()
}

// CPUsInNUMANodes returns all of the logical CPU IDs associated with the given
// NUMANode IDs in this CPUDetails.
func (d CPUDetails) CPUsInNUMANodes(ids ...int) cpuset.CPUSet {
	b := cpuset.NewBuilder()
	for _, id := range ids {
		for cpu, info := range d {
			if info.NUMANodeID == id {
				b.Add(cpu)
			}
		}
	}
	return b.Result()
}

// CPUsInCores returns all of the logical CPU IDs associated with the given
// core IDs in this CPUDetails.
func (d CPUDetails) CPUsInCores(ids ...int) cpuset.CPUSet {
	b := cpuset.NewBuilder()
	for _, id := range ids {
		for cpu, info := range d {
			if info.CoreID == id {
				b.Add(cpu)
			}
		}
	}
	return b.Result()
}

func transformNAdvisorNumaTopologyIntoNUMANodeInfo(numaTopology []nadvisor.Numa, numaNodeInfo NUMANodeInfo) {
	for numa := range numaNodeInfo {
		delete(numaNodeInfo, numa)
	}

	for _, numa := range numaTopology {
		threads := []int{}
		for _, core := range numa.Cores {
			for _, thread := range core.Threads {
				threads = append(threads, thread)
			}
		}

		numaNodeInfo[numa.Id] = numaNodeInfo[numa.Id].Union(cpuset.NewCPUSet(threads...))
	}
}

// Discover returns CPUTopology based on cadvisor node info
func Discover(machineInfo *cadvisorapi.MachineInfo, numaNodeInfo NUMANodeInfo) (*CPUTopology, error) {
	if numaNodeInfo == nil {
		return nil, fmt.Errorf("got nil numaNodeInfo in Discover")
	}

	// check if is aliyun, may be deprecated in the future
	if topoRefined, socketTopology, numaTopology, err := nadvisor.GetRefinedTopology(); err != nil {
		return nil, fmt.Errorf("get refined topology failed with error: %v", err)
	} else if topoRefined {
		if machineInfo == nil {
			return nil, fmt.Errorf("topology discover met nil machineInfo")
		} else if len(numaTopology) == 0 {
			return nil, fmt.Errorf("refined topology with empty numaTopology")
		}

		machineInfo.Topology = socketTopology

		klog.Infof("numaNodeInfo before refined: %+v", numaNodeInfo)
		transformNAdvisorNumaTopologyIntoNUMANodeInfo(numaTopology, numaNodeInfo)
		klog.Infof("numaNodeInfo after refined: %+v", numaNodeInfo)
	} else {
		klog.Infof("topology refined: %v; topology: %v", topoRefined, numaNodeInfo)
	}

	if machineInfo.NumCores == 0 {
		return nil, fmt.Errorf("could not detect number of cpus")
	}

	CPUDetails := CPUDetails{}
	numPhysicalCores := 0

	for _, node := range machineInfo.Topology {
		numPhysicalCores += len(node.Cores)
		for _, core := range node.Cores {
			if coreID, err := getUniqueCoreID(core.Threads); err == nil {
				for _, cpu := range core.Threads {
					CPUDetails[cpu] = CPUInfo{
						CoreID:     coreID,
						SocketID:   core.SocketID,
						NUMANodeID: node.Id,
					}
				}
			} else {
				klog.Errorf("could not get unique coreID for socket: %d core %d threads: %v",
					core.SocketID, core.Id, core.Threads)
				return nil, err
			}
		}
	}

	klog.Infof("discover cpu topology: NumCPUs: %d, NumSockets: %d, NumCores: %d, CPUDetails: %+v",
		machineInfo.NumCores, machineInfo.NumSockets, numPhysicalCores, CPUDetails)

	return &CPUTopology{
		NumCPUs:    machineInfo.NumCores,
		NumSockets: machineInfo.NumSockets,
		NumCores:   numPhysicalCores,
		CPUDetails: CPUDetails,
	}, nil
}

// getUniqueCoreID computes coreId as the lowest cpuID
// for a given Threads []int slice. This will assure that coreID's are
// platform unique (opposite to what cAdvisor reports - socket unique)
func getUniqueCoreID(threads []int) (coreID int, err error) {
	if len(threads) == 0 {
		return 0, fmt.Errorf("no cpus provided")
	}

	if len(threads) != cpuset.NewCPUSet(threads...).Size() {
		return 0, fmt.Errorf("cpus provided are not unique")
	}

	min := threads[0]
	for _, thread := range threads[1:] {
		if thread < min {
			min = thread
		}
	}

	return min, nil
}

// GetNUMANodeInfo uses sysfs to return a map of NUMANode id to the list of
// CPUs associated with that NUMANode.
//
// TODO: This is a temporary workaround until cadvisor provides this
// information directly in machineInfo. We should remove this once this
// information is available from cadvisor.
func GetNUMANodeInfo() (NUMANodeInfo, error) {
	// Get the possible NUMA nodes on this machine. If reading this file
	// is not possible, this is not an error. Instead, we just return a
	// nil NUMANodeInfo, indicating that no NUMA information is available
	// on this machine. This should implicitly be interpreted as having a
	// single NUMA node with id 0 for all CPUs.
	nodelist, err := ioutil.ReadFile("/sys/devices/system/node/online")
	if err != nil {
		return nil, nil
	}

	// Parse the nodelist into a set of Node IDs
	nodes, err := cpuset.Parse(strings.TrimSpace(string(nodelist)))
	if err != nil {
		return nil, err
	}

	info := make(NUMANodeInfo)

	// For each node...
	for _, node := range nodes.ToSlice() {
		// Read the 'cpulist' of the NUMA node from sysfs.
		path := fmt.Sprintf("/sys/devices/system/node/node%d/cpulist", node)
		cpulist, err := ioutil.ReadFile(path)
		if err != nil {
			return nil, err
		}

		// Convert the 'cpulist' into a set of CPUs.
		cpus, err := cpuset.Parse(strings.TrimSpace(string(cpulist)))
		if err != nil {
			return nil, err
		}

		info[node] = cpus
	}

	return info, nil
}
