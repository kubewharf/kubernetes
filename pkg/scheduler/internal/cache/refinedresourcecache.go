package cache

import (
	"fmt"
	"sort"
	"strings"
	"time"

	nnrv1alpha1 "code.byted.org/kubernetes/apis/k8s/non.native.resource/v1alpha1"
	nonnativeresourcelisters "code.byted.org/kubernetes/clientsets/k8s/listers/non.native.resource/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/klog"
	v1resource "k8s.io/kubernetes/pkg/api/v1/resource"
	"k8s.io/kubernetes/pkg/features"
	schedulernodeinfo "k8s.io/kubernetes/pkg/scheduler/nodeinfo"
	"k8s.io/kubernetes/pkg/scheduler/util"
)

func (cache *schedulerCache) AddOneVictim(deployName string, victimUID string) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if len(deployName) == 0 {
		// if the deployment name is empty, do not take any action
		return nil
	}

	if cache.deployVictims == nil {
		cache.deployVictims = make(map[string]victimSet)
	}
	if cache.deployVictims[deployName] == nil {
		cache.deployVictims[deployName] = make(victimSet)
	}
	cache.deployVictims[deployName][victimUID] = time.Now()
	return nil
}

func (cache *schedulerCache) SubtractOneVictim(deployName string, victimUID string) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if cache.deployVictims != nil && cache.deployVictims[deployName] != nil {
		delete(cache.deployVictims[deployName], victimUID)
	}

	return nil
}

func (cache *schedulerCache) ShouldDeployVictimsBeThrottled(pod *v1.Pod) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	deployName := util.GetDeployNameFromPod(pod)
	if len(deployName) == 0 {
		return false
	}

	return len(cache.deployVictims[deployName]) >= 5
}

func (cache *schedulerCache) CleanUpOutdatedVictims() {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	now := time.Now()
	for dpName, victims := range cache.deployVictims {
		for victimUID, evictionTime := range victims {
			if now.After(evictionTime.Add(1 * time.Minute)) {
				delete(cache.deployVictims[dpName], victimUID)
			}
			if len(cache.deployVictims[dpName]) == 0 {
				delete(cache.deployVictims, dpName)
			}
		}
	}
}

// Assumes that lock is already acquired.
func (cache *schedulerCache) addPreemptor(preemptor *v1.Pod) {
	n, ok := cache.nodes[preemptor.Status.NominatedNodeName]
	if !ok {
		n = newNodeInfoListItem(schedulernodeinfo.NewNodeInfo())
		cache.nodes[preemptor.Status.NominatedNodeName] = n
	}
	n.info.AddPod(preemptor)
	cache.moveNodeInfoToHead(preemptor.Status.NominatedNodeName)
}

// Assumes that lock is already acquired.
func (cache *schedulerCache) removePreemptor(preemptor *v1.Pod) error {
	if len(preemptor.Status.NominatedNodeName) == 0 {
		// if nominated node is not set, return directly
		return nil
	}

	n, ok := cache.nodes[preemptor.Status.NominatedNodeName]
	if !ok {
		return fmt.Errorf("node %v is not found", preemptor.Status.NominatedNodeName)
	}
	if err := n.info.RemovePod(preemptor); err != nil {
		// for backward compatibility, it may return error if the preemptor is not added to the NodeInfo
		// we can ignore the error
		// but if error occurs, no change operation will be performed, so returning directly is ok too
		return err
	}
	if len(n.info.Pods()) == 0 && n.info.Node() == nil {
		cache.removeNodeInfoFromList(preemptor.Status.NominatedNodeName)
	} else {
		cache.moveNodeInfoToHead(preemptor.Status.NominatedNodeName)
	}
	return nil
}

// make sure the preemptor.Status.NominatedNodeName is not nil when calling this
func (cache *schedulerCache) CachePreemptor(preemptor *v1.Pod) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if len(preemptor.Status.NominatedNodeName) == 0 {
		// if nominated node is not set, return directly
		return nil
	}

	// cache preemptor first
	// give 20 chances to schedule the preemptor to the nominated node
	cache.cachedPreemptors[preemptor.Namespace+"/"+preemptor.Name] = 20

	cache.addPreemptor(preemptor)

	return nil
}

func (cache *schedulerCache) PreemptorStillHaveChance(pod *v1.Pod) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	return cache.cachedPreemptors[pod.Namespace+"/"+pod.Name] > 0
}

func (cache *schedulerCache) ReduceOneChanceForPreemptor(preemptor *v1.Pod) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	/*previousCachedPreemptorCount := cache.cachedPreemptors[preemptor.Namespace+"/"+preemptor.Name]
	if previousCachedPreemptorCount == 0 {
		// delete cached preemptor anyway
		delete(cache.cachedPreemptors, preemptor.Namespace+"/"+preemptor.Name)
		// the previous cached preemptor count is 0,
		// the preemptor may be deleted from cached already or never cached, do not remove preemtor from node info
		// return directly
		return nil
	}*/

	if cache.cachedPreemptors[preemptor.Namespace+"/"+preemptor.Name] > 0 {
		cache.cachedPreemptors[preemptor.Namespace+"/"+preemptor.Name]--
	}

	// the preemptor is not cached (backward compatibility) or the chances has be run out
	if cache.cachedPreemptors[preemptor.Namespace+"/"+preemptor.Name] <= 0 {
		// delete cached preemptor first
		delete(cache.cachedPreemptors, preemptor.Namespace+"/"+preemptor.Name)

		// if the preemptor is not cached, there will be an error, but it doesn't matter
		cache.removePreemptor(preemptor)
	}

	return nil
}

func (cache *schedulerCache) DeletePreemptorFromCacheOnly(preemptor *v1.Pod) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	// delete cached preemptor only
	delete(cache.cachedPreemptors, preemptor.Namespace+"/"+preemptor.Name)

	return nil
}

func (cache *schedulerCache) DeletePreemptor(preemptor *v1.Pod) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	// delete cached preemptor first
	delete(cache.cachedPreemptors, preemptor.Namespace+"/"+preemptor.Name)

	cache.removePreemptor(preemptor)

	return nil
}

// NOTE: this function assumes lock has been acquired in caller
func (cache *schedulerCache) addRefinedResourceNode(refinedNodeResource *nnrv1alpha1.RefinedNodeResource) {
	if cache.refinedResourceNodes[refinedNodeResource.Name] != nil {
		delete(cache.refinedResourceNodes, refinedNodeResource.Name)
	}

	nrri := schedulernodeinfo.NewNodeRefinedResourceInfo(refinedNodeResource.Name)
	// TODO: do we need to check if DiscreteResource and NumericResource is nil ???

	// set discrete resource
	if refinedNodeResource.Spec.DiscreteResource.CPUProperties != nil {
		for _, pattern := range refinedNodeResource.Spec.DiscreteResource.CPUProperties {
			for _, propertyValue := range pattern.PropertyValues {
				nrri.AddDiscreteResourceProperty("cpu", pattern.PropertyName, propertyValue)
			}
		}
	}

	if refinedNodeResource.Spec.DiscreteResource.GPUProperties != nil {
		for _, pattern := range refinedNodeResource.Spec.DiscreteResource.GPUProperties {
			for _, propertyValue := range pattern.PropertyValues {
				nrri.AddDiscreteResourceProperty("gpu", pattern.PropertyName, propertyValue)
			}
		}
	}

	if refinedNodeResource.Spec.DiscreteResource.MemoryProperties != nil {
		for _, pattern := range refinedNodeResource.Spec.DiscreteResource.MemoryProperties {
			for _, propertyValue := range pattern.PropertyValues {
				nrri.AddDiscreteResourceProperty("memory", pattern.PropertyName, propertyValue)
			}
		}
	}

	if refinedNodeResource.Spec.DiscreteResource.DiskProperties != nil {
		for _, pattern := range refinedNodeResource.Spec.DiscreteResource.DiskProperties {
			for _, propertyValue := range pattern.PropertyValues {
				nrri.AddDiscreteResourceProperty("disk", pattern.PropertyName, propertyValue)
			}
		}
	}

	if refinedNodeResource.Spec.DiscreteResource.NetworkProperties != nil {
		for _, pattern := range refinedNodeResource.Spec.DiscreteResource.NetworkProperties {
			for _, propertyValue := range pattern.PropertyValues {
				nrri.AddDiscreteResourceProperty("network", pattern.PropertyName, propertyValue)
			}
		}
	}

	if refinedNodeResource.Spec.DiscreteResource.OtherProperties != nil {
		for _, pattern := range refinedNodeResource.Spec.DiscreteResource.OtherProperties {
			for _, propertyValue := range pattern.PropertyValues {
				nrri.AddDiscreteResourceProperty("other", pattern.PropertyName, propertyValue)
			}
		}
	}

	// set numeric resource
	if refinedNodeResource.Status.NumericResource.NumericProperties != nil {
		for _, pattern := range refinedNodeResource.Status.NumericResource.NumericProperties {
			nrri.AddNumericResourceProperties(pattern.PropertyName, pattern.PropertyAllocatableValue)
		}
	}

	if refinedNodeResource.Status.NumaStatus.Sockets != nil {
		numaIndex := 0
		for socketID, socketStatus := range refinedNodeResource.Status.NumaStatus.Sockets {
			for _, numaStatus := range socketStatus.Numas {
				nrri.AddNumaTopologyStatus(socketID, numaIndex, numaStatus.User)
				numaIndex++
			}
		}
	}

	cache.refinedResourceNodes[refinedNodeResource.Name] = nrri
}

func (cache *schedulerCache) AddRefinedResourceNode(refinedNodeResource *nnrv1alpha1.RefinedNodeResource) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.addRefinedResourceNode(refinedNodeResource)

	return nil
}

func (cache *schedulerCache) UpdateRefinedResourceNode(oldRefinedNodeResource, newRefinedNodeResource *nnrv1alpha1.RefinedNodeResource) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.deleteRefinedResourceNode(oldRefinedNodeResource)

	cache.addRefinedResourceNode(newRefinedNodeResource)

	return nil
}

// NOTE: this function assumes lock has been acquired in caller
func (cache *schedulerCache) deleteRefinedResourceNode(refinedNodeResource *nnrv1alpha1.RefinedNodeResource) {
	if cache.refinedResourceNodes[refinedNodeResource.Name] == nil {
		klog.Errorf("refined resource node: %s does not exist in cache", refinedNodeResource.Name)
	} else {
		delete(cache.refinedResourceNodes, refinedNodeResource.Name)
	}
}

func (cache *schedulerCache) DeleteRefinedResourceNode(refinedNodeResource *nnrv1alpha1.RefinedNodeResource) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.deleteRefinedResourceNode(refinedNodeResource)
	return nil
}

func (cache *schedulerCache) CleanupFilteredNodesForDeploy() {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	now := time.Now()
	if cache.filteredNodesForDP != nil {
		for dpName, nodesSet := range cache.filteredNodesForDP {
			if len(nodesSet) == 0 {
				delete(cache.filteredNodesForDP, dpName)
			} else {
				for nodeName, tmpInfo := range nodesSet {
					if now.After(tmpInfo.settingTime.Add(1 * time.Minute)) {
						delete(cache.filteredNodesForDP[dpName], nodeName)
						if len(cache.filteredNodesForDP[dpName]) == 0 {
							delete(cache.filteredNodesForDP, dpName)
						}
					}
				}
			}
		}
	}
}

// NOTE: this function assumes lock has been acquired in caller
func (cache *schedulerCache) addFilteredNodeForDeploy(deployName, nodeName string) {
	if cache.filteredNodesForDP == nil {
		cache.filteredNodesForDP = make(map[string]NodesSet)
	}
	if cache.filteredNodesForDP[deployName] == nil {
		cache.filteredNodesForDP[deployName] = make(NodesSet)
	}
	cache.filteredNodesForDP[deployName][nodeName] = nodeTmpInfo{
		settingTime: time.Now(),
	}
}

// TODO: processing in parallel
func (cache *schedulerCache) FilterNodesByPodRefinedResourceRequest(pod *v1.Pod, nodes []*schedulernodeinfo.NodeInfo, refinedNodeLister nonnativeresourcelisters.RefinedNodeResourceLister) []string {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	var filteredNodes []string

	// if the pod does not require refined resource, return all nodes directly
	if !utilfeature.DefaultFeatureGate.Enabled(features.NonNativeResourceSchedulingSupport) || !podRequestRefinedResources(pod) {
		filteredNodes = make([]string, len(nodes))
		i := 0
		for _, node := range nodes {
			//filteredNodes = append(filteredNodes, node.Name)
			if nodeObj := node.Node(); nodeObj != nil {
				filteredNodes[i] = nodeObj.Name
				i++
			}
		}
		return filteredNodes
	}

	deployName := util.GetDeployNameFromPod(pod)
	var deployNameGot bool
	if len(deployName) != 0 {
		deployNameGot = true
	}

	if deployNameGot {
		// if we have already cached for this deploy, return directly
		if cache.filteredNodesForDP[deployName] != nil && len(cache.filteredNodesForDP[deployName]) > 0 {
			filteredNodes = make([]string, len(cache.filteredNodesForDP[deployName]))
			i := 0
			for nodeName := range cache.filteredNodesForDP[deployName] {
				// filteredNodes = append(filteredNodes, nodeName)
				filteredNodes[i] = nodeName
				i++
			}
			klog.V(4).Infof("skipping pre-predicate checking, getting filtered nodes from cache")
			return filteredNodes
		}
	}

	// pre-filtering nodes based on pod's refined resource requests
	filteredNodes = make([]string, len(nodes))
	i := 0
	for _, node := range nodes {
		if node.Node() == nil {
			continue
		}

		if cache.refinedResourceNodes[node.Node().Name] == nil && refinedNodeLister != nil {
			if refinedNode, getErr := refinedNodeLister.Get(node.Node().Name); getErr == nil {
				cache.addRefinedResourceNode(refinedNode)
			}
		}

		if cache.refinedResourceNodes[node.Node().Name] != nil && NodeMatchedPodRequest(pod, cache.refinedResourceNodes[node.Node().Name]) {
			filteredNodes[i] = node.Node().Name
			i++
			if deployNameGot {
				cache.addFilteredNodeForDeploy(deployName, node.Node().Name)
			}
		}
	}

	return filteredNodes[:i]
}

// NOTE: this function assumes lock has been acquired in caller
func podRequestRefinedResources(pod *v1.Pod) bool {
	if pod.Annotations == nil {
		return false
	}

	if len(pod.Annotations[util.CpuPropertiesRequests]) > 0 || len(pod.Annotations[util.GpuPropertiesRequests]) > 0 || len(pod.Annotations[util.DiskPropertiesRequests]) > 0 ||
		len(pod.Annotations[util.MemoryPropertiesRequests]) > 0 || len(pod.Annotations[util.NetworkPropertiesRequests]) > 0 || len(pod.Annotations[util.OtherPropertiesRequests]) > 0 {
		return true
	}

	if len(pod.Annotations[util.NumericResourcesRequests]) > 0 {
		return true
	}

	return false
}

func NodeMatchedPodRequest(pod *v1.Pod, refinedResourceInfo *schedulernodeinfo.NodeRefinedResourceInfo) bool {
	if pod.Annotations == nil {
		return true
	}
	if len(pod.Annotations[util.CpuPropertiesRequests]) > 0 {
		if !parseDiscreteResourcesProperties(util.CpuPropertiesRequests, pod.Annotations[util.CpuPropertiesRequests], refinedResourceInfo) {
			return false
		}
	}
	if len(pod.Annotations[util.GpuPropertiesRequests]) > 0 {
		if !parseDiscreteResourcesProperties(util.GpuPropertiesRequests, pod.Annotations[util.GpuPropertiesRequests], refinedResourceInfo) {
			return false
		}
	}
	if len(pod.Annotations[util.DiskPropertiesRequests]) > 0 {
		if !parseDiscreteResourcesProperties(util.DiskPropertiesRequests, pod.Annotations[util.DiskPropertiesRequests], refinedResourceInfo) {
			return false
		}
	}
	if len(pod.Annotations[util.MemoryPropertiesRequests]) > 0 {
		if !parseDiscreteResourcesProperties(util.MemoryPropertiesRequests, pod.Annotations[util.MemoryPropertiesRequests], refinedResourceInfo) {
			return false
		}
	}

	if len(pod.Annotations[util.NetworkPropertiesRequests]) > 0 {
		if !parseDiscreteResourcesProperties(util.NetworkPropertiesRequests, pod.Annotations[util.NetworkPropertiesRequests], refinedResourceInfo) {
			return false
		}
	}

	if len(pod.Annotations[util.OtherPropertiesRequests]) > 0 {
		if !parseDiscreteResourcesProperties(util.OtherPropertiesRequests, pod.Annotations[util.OtherPropertiesRequests], refinedResourceInfo) {
			return false
		}
	}

	// check numeric resource
	if len(pod.Annotations[util.NumericResourcesRequests]) > 0 {
		if !parseNumericResourcesProperties(pod.Annotations[util.NumericResourcesRequests], refinedResourceInfo, pod) {
			return false
		}
	}

	return true
}

func NumaTopologyMatchedPodRequest(pod *v1.Pod, refinedResourceInfo *schedulernodeinfo.NodeRefinedResourceInfo, nodeInfo *schedulernodeinfo.NodeInfo) bool {
	// if feature gate is disable, skip the predicate check
	if !utilfeature.DefaultFeatureGate.Enabled(features.NonNativeResourceSchedulingSupport) {
		return true
	}
	if requestNuma := v1resource.GetResourceRequest(pod, v1.ResourceBytedanceSocket); requestNuma == 0 {
		return true
	}
	if refinedResourceInfo == nil {
		return false
	}
	// Check cpus per numa & mems per numa
	if !satisfyResourcesPerNuma(nodeInfo, pod) {
		return false
	}
	// Check numa topology
	if !satisfyNumaBalance(refinedResourceInfo, nodeInfo, pod) {
		return false
	}

	return true
}

// property values are split by different symbols
// "|" => "OR"
// "&" => "AND"
// nothing => "Equal"
// TODO: add properties string validation
func parseDiscreteResourcesProperties(propertyKey, properties string, refinedResourceInfo *schedulernodeinfo.NodeRefinedResourceInfo) bool {
	var resourceProperties map[string]sets.String
	switch propertyKey {
	case util.CpuPropertiesRequests:
		resourceProperties = refinedResourceInfo.GetCPUProperties()
	case util.GpuPropertiesRequests:
		resourceProperties = refinedResourceInfo.GetGPUProperties()
	case util.DiskPropertiesRequests:
		resourceProperties = refinedResourceInfo.GetDiskProperties()
	case util.MemoryPropertiesRequests:
		resourceProperties = refinedResourceInfo.GetMemoryProperties()
	case util.NetworkPropertiesRequests:
		resourceProperties = refinedResourceInfo.GetNetworkProperties()
	case util.OtherPropertiesRequests:
		resourceProperties = refinedResourceInfo.GetOtherProperties()
	}
	// resourceProperties won't be nil because of the initialization

	andProperties := strings.Split(properties, "&")
	for _, andProperty := range andProperties {
		orProperties := strings.Split(andProperty, "|")
		OrResult := false
		for _, orProperty := range orProperties {
			kv := strings.Split(orProperty, "=")
			if len(kv) != 2 {
				fmt.Errorf("properties format error")
				return false
			} else {
				if resourceProperties[kv[0]] == nil {
					return false
				} else {
					// OrResult = OrResult || resourceProperties[kv[0]].Has(kv[1])
					if resourceProperties[kv[0]].Has(kv[1]) {
						// if we meet one request, skip the following checks to save time
						// since they are "OR" pattern
						OrResult = true
						break
					}
				}
			}
		}
		if !OrResult {
			// if one of the "AND" properties is false. return false directly
			return false
		}
	}
	return true
}

func parseNumericResourcesProperties(properties string, refinedResourceInfo *schedulernodeinfo.NodeRefinedResourceInfo, pod *v1.Pod) bool {
	var resourceCapacity map[string]int64
	resourceCapacity = refinedResourceInfo.GetNumericResourceProperties()

	andProperties := strings.Split(properties, "&")
	for _, andProperty := range andProperties {
		orProperties := strings.Split(andProperty, "|")
		OrResult := false
		for _, orProperty := range orProperties {
			// TODO: for now, only support ">=" operator
			// support more later if needed
			kv := strings.Split(orProperty, ">=")
			if len(kv) != 2 {
				fmt.Errorf("properties format error")
				return false
			} else {
				capacity, ok := resourceCapacity[kv[0]]
				if !ok {
					return false
				} else {
					request, err := resource.ParseQuantity(kv[1])
					if err != nil {
						// this should not happen
						// TODO: panic here ?
						klog.Errorf("parse quantity for %s error: %v", kv[0], err)
						return false
					}

					if kv[0] == util.CPURefinedResourceKey || kv[0] == util.MemoryRefineResourceKey {
						// if the key is CPU or Memory, it must be numa/socket applications
						// take container request resource and node capacity into account
						if satisfyCPUMemRequest(kv[0], request, pod, refinedResourceInfo) {
							// if we meet one request, skip the following checks to save time
							// since they are "OR" pattern
							OrResult = true
							break
						}
					} else {
						// if the key is not CPU or Memory, just check the value
						if capacity >= request.Value() {
							// if we meet one request, skip the following checks to save time
							// since they are "OR" pattern
							OrResult = true
							break
						}
					}
				}
			}
		}
		if !OrResult {
			// if one of the "AND" properties is false. return false directly
			return false
		}
	}
	return true
}

func satisfyCPUMemRequest(name string, request resource.Quantity, pod *v1.Pod, refinedResourceInfo *schedulernodeinfo.NodeRefinedResourceInfo) bool {
	// TODO: populate the check method
	capacityMap := refinedResourceInfo.GetNumericResourceProperties()
	resourceCapacity := capacityMap[name]

	numaCapacity := capacityMap[util.NumaRefinedResourceKey]

	if numaCapacity <= 0 {
		return false
	}

	// get numa num from container request
	requestedNumaNum := v1resource.GetResourceRequest(pod, v1.ResourceBytedanceSocket)

	if requestedNumaNum <= 0 {
		// for now, if numa is not requested, CPU and Memory numeric checking should not happen
		// return true directly
		// TODO: revisit this if needed
		return true
	}

	if name == util.CPURefinedResourceKey {
		if resourceCapacity/numaCapacity*requestedNumaNum >= request.MilliValue() {
			return true
		} else {
			return false
		}
	} else {
		// name = util.MemoryRefineResourceKey
		if resourceCapacity/numaCapacity*requestedNumaNum >= request.Value() {
			return true
		} else {
			return false
		}
	}
}

func satisfyResourcesPerNuma(nodeInfo *schedulernodeinfo.NodeInfo, pod *v1.Pod) bool {
	requestCPU := v1resource.GetResourceRequest(pod, v1.ResourceCPU)
	requestMem := v1resource.GetResourceRequest(pod, v1.ResourceMemory)
	requestNuma := v1resource.GetResourceRequest(pod, v1.ResourceBytedanceSocket)
	requestGPU := v1resource.GetResourceRequest(pod, util.ResourceGPU)
	allocatableResource := nodeInfo.AllocatableResource()
	allocCPU := allocatableResource.MilliCPU
	allocMem := allocatableResource.Memory
	allocNuma := allocatableResource.ScalarResources[v1.ResourceBytedanceSocket]
	allocGPU := allocatableResource.ScalarResources[util.ResourceGPU]
	if requestNuma == 0 {
		return true
	}
	if ((requestCPU-1)/requestNuma+1)*allocNuma > allocCPU {
		return false
	}
	if ((requestMem-1)/requestNuma+1)*allocNuma > allocMem {
		return false
	}
	// in order to satisfy numa-gpu affinity
	// requestNuma/allocNuma should be equal to requestGPU/allocGPU
	if requestGPU > 0 && requestNuma > 0 && requestGPU*allocNuma != requestNuma*allocGPU {
		return false
	}
	return true
}

func satisfyNumaBalance(refinedResourceInfo *schedulernodeinfo.NodeRefinedResourceInfo, nodeInfo *schedulernodeinfo.NodeInfo, pod *v1.Pod) bool {
	requestNuma := v1resource.GetResourceRequest(pod, v1.ResourceBytedanceSocket)
	if requestNuma == 0 {
		return true
	}
	numaTopologyStatus := refinedResourceInfo.GetNumaTopologyStatus()
	numaNum := numaTopologyStatus.GetNumaNum()
	socketNum := numaTopologyStatus.GetSocketNum()
	if numaNum == 0 || socketNum == 0 {
		return false
	}
	numaNumPerSocket := numaNum / socketNum
	requestSocket := (requestNuma-1)/int64(numaNumPerSocket) + 1
	freeNumasInSockets := numaTopologyStatus.GetFreeNumasInSockets()
	var socketSlice []int
	for socketID := range freeNumasInSockets {
		socketSlice = append(socketSlice, socketID)
	}
	sort.Slice(socketSlice, func(i, j int) bool {
		return freeNumasInSockets[socketSlice[i]].Len() > freeNumasInSockets[socketSlice[j]].Len()
	})
	freeNumasNum := 0
	for i, socketID := range socketSlice {
		if int64(i) >= requestSocket {
			return false
		}
		freeNumasNum += freeNumasInSockets[socketID].Len()
		if int64(freeNumasNum) >= requestNuma {
			return true
		}
	}
	return false
}

func (cache *schedulerCache) GetRefinedResourceNode(nodeName string) *schedulernodeinfo.NodeRefinedResourceInfo {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, ok := cache.refinedResourceNodes[nodeName]; !ok {
		return nil
	}
	return cache.refinedResourceNodes[nodeName].Clone()
}
