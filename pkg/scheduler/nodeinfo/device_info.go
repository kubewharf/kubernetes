package nodeinfo

import (
	"fmt"
	"sync"

	"k8s.io/klog"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/kubernetes/pkg/scheduler/util"
)

type NodeShareGPUDeviceInfo struct {
	devs           map[int]*DeviceInfo
	gpuCount       int
	gpuTotalMemory int
	sync.RWMutex
	name string
}

func (n *NodeShareGPUDeviceInfo) GetTotalGPUMemory() int {
	return n.gpuTotalMemory
}

func (n *NodeShareGPUDeviceInfo) GetGPUCount() int {
	return n.gpuCount
}

func (n *NodeShareGPUDeviceInfo) SetShareGPUDevices(node *v1.Node) {
	// node has no allocatable share gpu
	if !util.IsGPUSharingNode(node) {
		return
	}
	// NodeShareGPUDeviceInfo has been initialized
	if n.gpuCount > 0 {
		return
	}
	n.Lock()
	defer n.Unlock()

	// set share gpu device info in node info
	devMap := map[int]*DeviceInfo{}
	for i := 0; i < util.GetGPUCountInNode(node); i++ {
		devMap[i] = newDeviceInfo(i, util.GetTotalGPUMemory(node)/util.GetGPUCountInNode(node), util.GetTotalGPUSM(node)/util.GetGPUCountInNode(node))
	}
	n.devs = devMap
	n.gpuCount = util.GetGPUCountInNode(node)
	n.gpuTotalMemory = util.GetTotalGPUMemory(node)
	n.name = node.Name
}

func (n *NodeShareGPUDeviceInfo) removePodDevices(pod *v1.Pod) {
	id := util.GetGPUIDFromAnnotation(pod)
	if id >= 0 {
		n.RLock()
		defer n.RUnlock()
		dev := n.devs[id]
		if dev != nil {
			klog.Infof("debug: removePodDevices() Pod %s in ns %s with the GPU ID %d should be removed from device map",
				pod.Name, pod.Namespace, id)
			dev.removePod(pod)
		}
	}
}

// Add the Pod which has the GPU id to the node
func (n *NodeShareGPUDeviceInfo) addPodDevices(pod *v1.Pod) {
	// if pod has completed, remove devices
	if util.IsCompletePod(pod) {
		n.removePodDevices(pod)
		return
	}
	id := util.GetGPUIDFromAnnotation(pod)
	if id >= 0 {
		n.RLock()
		defer n.RUnlock()

		dev := n.devs[id]
		if dev != nil {
			klog.Infof("debug: addPodDevices() Pod %s in ns %s with the GPU ID %d should be added to device map",
				pod.Name, pod.Namespace, id)
			dev.addPod(pod)
		}
	}
}

// check if the pod can be allocated on the node
func (n *NodeShareGPUDeviceInfo) SatisfyShareGPU(pod *v1.Pod) bool {
	n.RLock()
	defer n.RUnlock()

	availableGPUs := n.getAvailableGPUs()
	reqGPUMem := util.GetGPUMemoryFromPodResource(pod)
	reqGPUSM := util.GetGPUSMFromPodResource(pod)
	if len(availableGPUs) > 0 {
		for devID := 0; devID < len(n.devs); devID++ {
			availableGPU, ok := availableGPUs[devID]
			if ok {
				if availableGPU.Memory >= reqGPUMem && availableGPU.SM >= reqGPUSM {
					return true
				}
			}
		}
	}

	return false
}

func (n *NodeShareGPUDeviceInfo) AllocateShareGPU(pod *v1.Pod) (err error) {
	// if pod resource has no requirement for share gpu (including gpu-sm and gpu memory), just return
	if !util.IsGPUSharingPod(pod) {
		return nil
	}
	n.RLock()
	defer n.RUnlock()
	klog.Infof("NodeShareGPUDeviceInfo: Allocate() ----Begin to allocate GPU for gpu mem for pod %s in ns %s----", pod.Name, pod.Namespace)
	devId, ok := n.allocateGPUID(pod)
	if ok {
		klog.Infof("NodeShareGPUDeviceInfo: Allocate GPU ID %d to pod %s in ns %s.----", devId, pod.Name, pod.Namespace)
		util.AddPodAnnotation(pod, devId)
		klog.Infof("NodeShareGPUDeviceInfo: Try to add pod %s in ns %s to dev %d", pod.Name, pod.Namespace, devId)
		dev, found := n.devs[devId]
		if !found {
			klog.Infof("warn: Pod %s in ns %s failed to find the GPU ID %d in node %s", pod.Name, pod.Namespace, devId, n.name)
		} else {
			dev.addPod(pod)
		}
	} else {
		err = fmt.Errorf("can't place the pod %s in ns %s because share gpu not satisfied", pod.Name, pod.Namespace)
	}

	return err
}

// allocate the GPU ID to the pod
func (n *NodeShareGPUDeviceInfo) allocateGPUID(pod *v1.Pod) (candidateDevID int, found bool) {
	found = false
	candidateDevID = -1
	candidateAvailableGPU := &GPUResource{}

	availableGPUs := n.getAvailableGPUs()
	reqGPUMem := util.GetGPUMemoryFromPodResource(pod)
	reqGPUSM := util.GetGPUSMFromPodResource(pod)

	if reqGPUMem > 0 || reqGPUSM > 0 {
		klog.Infof("reqGPU for pod %s in ns %s:gpu-mem %dGi gpu-sm %d", pod.Name, pod.Namespace, reqGPUMem, reqGPUSM)
		if len(availableGPUs) > 0 {
			for devID := 0; devID < len(n.devs); devID++ {
				availableGPU, ok := availableGPUs[devID]
				if ok && availableGPU.Memory >= reqGPUMem && availableGPU.SM >= reqGPUSM {
					if candidateDevID == -1 || candidateAvailableGPU.SM > availableGPU.SM {
						candidateDevID = devID
						candidateAvailableGPU = availableGPU
					}
					found = true
				}
			}
		}

		if found {
			klog.Infof("Find candidate dev id %d for pod %s in ns %s successfully.",
				candidateDevID, pod.Name, pod.Namespace)
		} else {
			klog.Infof("Failed to find available GPUs memory %d sm %d for the pod %s in the namespace %s",
				reqGPUMem, reqGPUSM, pod.Name, pod.Namespace)
		}
	}

	return candidateDevID, found
}

func (n *NodeShareGPUDeviceInfo) getAvailableGPUs() map[int]*GPUResource {
	allGPUs := n.getAllGPU()
	usedGPUs := n.getUsedGPU()
	// gpu device id: resource name: quantity
	availableGPUResource := make(map[int]*GPUResource)
	for id, gpuResource := range allGPUs {
		availableGPUResource[id] = &GPUResource{
			Memory: gpuResource.Memory,
			SM:     gpuResource.SM,
		}
		if usedGpuResource, found := usedGPUs[id]; found {
			availableGPUResource[id] = &GPUResource{
				Memory: gpuResource.Memory - usedGpuResource.Memory,
				SM:     gpuResource.SM - usedGpuResource.SM,
			}
		}
	}
	klog.Infof("NodeShareGPUDeviceInfo getAvailableGPUs: %+v in node %s", availableGPUResource, n.name)
	return availableGPUResource
}

// device index: gpu resource
func (n *NodeShareGPUDeviceInfo) getUsedGPU() map[int]*GPUResource {
	usedGPUResources := make(map[int]*GPUResource)
	for _, dev := range n.devs {
		usedGPUResources[dev.idx] = dev.GetUsedGPU()
	}
	return usedGPUResources
}

// device index: gpu resource
func (n *NodeShareGPUDeviceInfo) getAllGPU() map[int]*GPUResource {
	allGPUResource := make(map[int]*GPUResource)
	for _, dev := range n.devs {
		allGPUResource[dev.idx] = &GPUResource{
			SM:     dev.deviceGPUSM,
			Memory: dev.deviceGPUMem,
		}
	}
	return allGPUResource
}

func (n *NodeShareGPUDeviceInfo) Clone() *NodeShareGPUDeviceInfo {
	n.RLock()
	defer n.RUnlock()

	cloneDevs := make(map[int]*DeviceInfo)
	for idx, deviceInfo := range n.devs {
		cloneDevs[idx] = deviceInfo.Clone()
	}
	clone := &NodeShareGPUDeviceInfo{
		devs:           cloneDevs,
		gpuCount:       n.gpuCount,
		gpuTotalMemory: n.gpuTotalMemory,
		name:           n.name,
	}
	return clone
}

type DeviceInfo struct {
	idx          int
	podMap       map[types.UID]*v1.Pod
	deviceGPUMem int
	deviceGPUSM  int
	rwmu         *sync.RWMutex
}

func (d *DeviceInfo) GetPods() []*v1.Pod {
	d.rwmu.RLock()
	defer d.rwmu.RUnlock()
	var pods []*v1.Pod
	for _, pod := range d.podMap {
		pods = append(pods, pod)
	}
	return pods
}

func newDeviceInfo(index int, deviceGPUMem int, deviceGPUSM int) *DeviceInfo {
	return &DeviceInfo{
		idx:          index,
		deviceGPUMem: deviceGPUMem,
		deviceGPUSM:  deviceGPUSM,
		podMap:       map[types.UID]*v1.Pod{},
		rwmu:         new(sync.RWMutex),
	}
}

func (d *DeviceInfo) GetDeviceGPUMemory() int {
	return d.deviceGPUMem
}

func (d *DeviceInfo) GetDeviceGPUSM() int {
	return d.deviceGPUSM
}

func (d *DeviceInfo) GetUsedGPU() *GPUResource {
	var podNamesOnDevice []string
	for _, pod := range d.podMap {
		podNamesOnDevice = append(podNamesOnDevice, pod.Name)
	}
	klog.Infof("[gpu device info] GetUsedGPUMemory() pods %+q, and device id is %d", podNamesOnDevice, d.idx)
	gpuResource := new(GPUResource)
	d.rwmu.RLock()
	defer d.rwmu.RUnlock()
	for _, pod := range d.podMap {
		if pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed {
			klog.Infof("[gpu device info] skip the pod %s in ns %s due to its status is %s", pod.Name, pod.Namespace, pod.Status.Phase)
			continue
		}
		gpuResource.Memory += util.GetGPUMemoryFromPodResource(pod)
		gpuResource.SM += util.GetGPUSMFromPodResource(pod)
	}
	return gpuResource
}

func (d *DeviceInfo) addPod(pod *v1.Pod) {
	klog.Infof("[gpu device info] dev.addPod() Pod %s in ns %s with the GPU ID %d will be added to device map",
		pod.Name, pod.Namespace, d.idx)
	d.rwmu.Lock()
	defer d.rwmu.Unlock()
	d.podMap[pod.UID] = pod
	klog.V(8).Infof("[gpu device info] dev.addPod() after updated is %v, and device id is %d", d.podMap, d.idx)
}

func (d *DeviceInfo) removePod(pod *v1.Pod) {
	klog.Infof("[gpu device info] dev.removePod() Pod %s in ns %s with the GPU ID %d will be removed from device map",
		pod.Name, pod.Namespace, d.idx)
	d.rwmu.Lock()
	defer d.rwmu.Unlock()
	delete(d.podMap, pod.UID)
	klog.V(8).Infof("[gpu device info] dev.removePod() after updated is %v, device id is %d", d.podMap, d.idx)
}

func (d *DeviceInfo) Clone() *DeviceInfo {
	d.rwmu.RLock()
	defer d.rwmu.RUnlock()

	podMapClone := make(map[types.UID]*v1.Pod)
	for uid, pod := range d.podMap {
		podMapClone[uid] = pod.DeepCopy()
	}
	clone := &DeviceInfo{
		idx:          d.idx,
		podMap:       podMapClone,
		deviceGPUMem: d.deviceGPUMem,
		deviceGPUSM:  d.deviceGPUSM,
		rwmu:         new(sync.RWMutex),
	}
	return clone
}

type GPUResource struct {
	Memory int
	SM     int
}

func (gpuResource *GPUResource) String() string {
	return fmt.Sprintf("(sm%d, memory %d)", gpuResource.SM, gpuResource.Memory)
}
