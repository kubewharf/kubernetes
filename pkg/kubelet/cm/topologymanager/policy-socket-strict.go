package topologymanager

import (
	"errors"

	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog"

	kubefeatures "k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager/bitmask"
)

type socketStrictPolicy struct {
	// key is numa id value is socket id
	numaToSocket map[int]int
	// numa size in socket
	numaSizePerSocket int
	//List of NUMA Nodes available on the underlying machine
	numaNodes []int
}

var _ Policy = &socketStrictPolicy{}

const (
	PolicySocketStrict string = "socket-strict"
	SysprobeNumaPlugin string = "bytedance.com/socket"
)

func NewSocketStrictPolicy(topology *topology.CPUTopology, numaNodes []int) Policy {
	// key is numa id, value is socket id
	numaInfo := make(map[int]int)
	for _, cpuInfo := range topology.CPUDetails {
		numaInfo[cpuInfo.NUMANodeID] = cpuInfo.SocketID
	}
	numaSizePerSocket := topology.CPUDetails.NUMANodes().Size() / topology.CPUDetails.Sockets().Size()
	klog.Infof("[policy-socket-strict] numa to socket info is %v, numaSizePerSocket is %d", numaInfo, numaSizePerSocket)
	return &socketStrictPolicy{numaToSocket: numaInfo, numaSizePerSocket: numaSizePerSocket, numaNodes: numaNodes}
}

func (p *socketStrictPolicy) Name() string {
	return PolicySocketStrict
}

func (p *socketStrictPolicy) canAdmitPodResult(hint *TopologyHint) bool {
	return hint.Preferred
}

// get current cpu topology status by hint from providers
func (p *socketStrictPolicy) getTopoStatusFromHints(numaDevicehints []TopologyHint) (map[int]sets.Int, int, error) {
	// container numa request
	request := 0
	// key is socket id, value is numa id set in socket
	currentSocketNumaStatus := make(map[int]sets.Int)

	// if hint preferred, it's affinity count should be equal to numa request
	// if multi preferred hint has different count, we conclude hint provider has bug
	for _, hint := range numaDevicehints {
		if hint.Preferred {
			if request == 0 {
				request = hint.NUMANodeAffinity.Count()
			} else if request != hint.NUMANodeAffinity.Count() {
				klog.Error("[policy-socket-strict] get different numa count from multi preferred hint")
				return nil, 0, errors.New("can't get correct numa request from provider hints")
			}
			// if prefered is true and numa is in hint, it mean's the numa is still available
			for numaId, socketId := range p.numaToSocket {
				if hint.NUMANodeAffinity.IsSet(numaId) {
					if _, ok := currentSocketNumaStatus[socketId]; !ok {
						currentSocketNumaStatus[socketId] = sets.NewInt()
					}
					currentSocketNumaStatus[socketId].Insert(numaId)
				}
			}
		}
	}

	return currentSocketNumaStatus, request, nil
}

// numa allocation logic aligned with sysprobe
func (p *socketStrictPolicy) takeForNumaNode(providersHints []map[string][]TopologyHint) *TopologyHint {
	var numaDevicehints []TopologyHint = nil
	// filter hints provided by sysprobe numa device plugin
	for _, providerHints := range providersHints {
		for resource, hints := range providerHints {
			if resource == SysprobeNumaPlugin {
				numaDevicehints = hints
				break
			}
		}
	}
	defaultAffinity, _ := bitmask.NewBitMask(p.numaNodes...)
	if numaDevicehints == nil {
		return &TopologyHint{NUMANodeAffinity: defaultAffinity, Preferred: true}
	}
	// no numa hint provided, which means numa resource is not sufficient.
	if len(numaDevicehints) == 0 {
		klog.Infof("[policy-socket-strict] numa resource is not sufficient. return topology hint with prefered set false")
		return &TopologyHint{NUMANodeAffinity: defaultAffinity, Preferred: false}
	}
	// if aep numa awareness enabled, allocated aep device has higher priority
	if utilfeature.DefaultFeatureGate.Enabled(kubefeatures.AepCSITopologyAware) {
		var aepDevicehints []TopologyHint = nil
		// filter hints provided by sysprobe numa device plugin
		for _, providerHints := range providersHints {
			for resource, hints := range providerHints {
				if resource == AepCSI {
					aepDevicehints = hints
					break
				}
			}
		}
		if len(aepDevicehints) > 0 {
			aepCandidateAffinity := defaultAffinity
			for _, aepHint := range aepDevicehints {
				aepCandidateAffinity.And(aepHint.NUMANodeAffinity)
			}
			// it means aep device has already allocated
			if aepCandidateAffinity.Count() > 0 {
				hint := &TopologyHint{NUMANodeAffinity: aepCandidateAffinity, Preferred: true}
				klog.Infof("[policy-socket-strict] aep-csi-awared determined hint slice is %v", hint)
				return hint
			}
		}
	}
	// pre-calculate the desired topology hint
	socketNumaMap, numaRequirement, err := p.getTopoStatusFromHints(numaDevicehints)
	klog.Infof("[policy-socket-strict] socket numa status is %v, numa request is %d, err is %v", socketNumaMap, numaRequirement, err)
	if err != nil {
		return &TopologyHint{Preferred: false}
	}

	// select numa, derived from sysprobe logic
	numaNeed := numaRequirement
	selectedNumas := sets.NewInt()
	for numaNeed != 0 {
		// if require more than 1 socket
		if numaNeed >= p.numaSizePerSocket {
			for socket, numas := range socketNumaMap {
				if numas.Len() == p.numaSizePerSocket {
					selectedNumas = selectedNumas.Union(numas)
					socketNumaMap[socket] = sets.Int{}
					break
				} else if numas.Len() > p.numaSizePerSocket {
					klog.Infof("[policy-socket-strict] buggy numaPerSocket size, numas.Size: %d!", numas.Len())
				}
			}
		} else {
			selectedNumaSize := p.numaSizePerSocket
			selectedSocket := -1
			for socket, numas := range socketNumaMap {
				if numas.Len() >= numaNeed && numas.Len() <= selectedNumaSize {
					selectedNumaSize = numas.Len()
					selectedSocket = socket
				}
			}
			if selectedSocket != -1 {
				numaNeedForSocket := numaNeed
				selected := sets.NewInt()
				for key := range socketNumaMap[selectedSocket] {
					if numaNeedForSocket > 0 {
						selected.Insert(key)
						numaNeedForSocket -= 1
					}
				}
				socketNumaMap[selectedSocket] = socketNumaMap[selectedSocket].Difference(selected)
				selectedNumas = selectedNumas.Union(selected)
			}
		}
		klog.Infof("[policy-socket-strict] takeForNumaNode selectedNumas is %v", selectedNumas.List())
		if numaNeed == numaRequirement-selectedNumas.Len() {
			klog.Error("[policy-socket-strict] allocate numa failed!")
			return &TopologyHint{Preferred: false}
		}
		numaNeed = numaRequirement - selectedNumas.Len()
	}
	klog.Infof("[policy-socket-strict] allocate numa successful, advise is %v", selectedNumas.List())

	mask, err := bitmask.NewBitMask(selectedNumas.List()...)
	if err != nil {
		return &TopologyHint{Preferred: false}
	}
	return &TopologyHint{Preferred: true, NUMANodeAffinity: mask}
}

// if candidate topology can be got after  mergePermutation, we adopt it as only choice
func matchFilteredHints(numaNodes []int, filteredHints [][]TopologyHint, candidateHint TopologyHint) TopologyHint {
	// Set the default affinity as an any-numa affinity containing the list
	// of NUMA Nodes available on this machine.
	defaultAffinity, _ := bitmask.NewBitMask(numaNodes...)
	klog.Info("default affinity is ", defaultAffinity.String())
	// Set the bestHint to return from this function as {nil false}.
	// This will only be returned if no better hint can be found when
	// merging hints from each hint provider.
	bestHint := TopologyHint{defaultAffinity, false}
	iterateAllProviderTopologyHints(filteredHints, func(permutation []TopologyHint) {
		// Get the NUMANodeAffinity from each hint in the permutation and see if any
		// of them encode unpreferred allocations.
		mergedHint := mergePermutation(numaNodes, permutation)
		// Only consider mergedHints that result in a NUMANodeAffinity > 0 to
		// replace the current bestHint.
		if mergedHint.NUMANodeAffinity.Count() == 0 {
			return
		}

		// hint can only be sub set of candidate hint
		for _, bit := range mergedHint.NUMANodeAffinity.GetBits() {
			if !candidateHint.NUMANodeAffinity.IsSet(bit) {
				return
			}
		}

		// If the current bestHint is non-preferred and the new mergedHint is
		// preferred, always choose the preferred hint over the non-preferred one.
		// if both prefered, choose the max count to satisfy numa request
		if mergedHint.Preferred {
			if !bestHint.Preferred || mergedHint.NUMANodeAffinity.Count() > bestHint.NUMANodeAffinity.Count() {
				bestHint = mergedHint
				return
			}
		}

	})

	return bestHint
}

func (p *socketStrictPolicy) Merge(providersHints []map[string][]TopologyHint) (TopologyHint, bool) {
	for i, providerHint := range providersHints {
		for resource, hints := range providerHint {
			klog.Infof("[policy-socket-strict] provider index is %d, resource name is %s, hint slice is %v", i, resource, hints)
		}
	}

	candidateHint := p.takeForNumaNode(providersHints)
	klog.Infof("[policy-socket-strict] candidate hint from policy is %v", candidateHint)

	if !candidateHint.Preferred {
		return *candidateHint, false
	}
	filteredHints := filterProvidersHints(providersHints)
	for i := range filteredHints {
		for j := range filteredHints[i] {
			klog.Infof("filtered hints [%d][%d] is %v", i, j, filteredHints[i][j])
		}
	}
	bestHint := matchFilteredHints(p.numaNodes, filteredHints, *candidateHint)
	klog.Infof("[policy-socket-strict] chosen best hint after merge is %v", bestHint)
	admit := p.canAdmitPodResult(&bestHint)
	return bestHint, admit
}
