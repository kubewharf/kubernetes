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

package qosresourcemanager

import (
	"fmt"
	"reflect"
	"strconv"
	"sync"

	"k8s.io/apimachinery/pkg/util/sets"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog"
	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
	"k8s.io/kubernetes/pkg/kubelet/cm/qosresourcemanager/checkpoint"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"

	"github.com/golang/protobuf/proto"
)

type ResourceAllocation map[string]*pluginapi.ResourceAllocationInfo // Keyed by resourceName.
type ContainerResources map[string]ResourceAllocation                // Keyed by containerName.
type PodResources map[string]ContainerResources                      // Keyed by podUID

type podResourcesChk struct {
	sync.RWMutex
	resources PodResources // Keyed by podUID.
}

var EmptyValue = reflect.Value{}

func newPodResourcesChk() *podResourcesChk {
	return &podResourcesChk{
		resources: make(PodResources),
	}
}

func (pr PodResources) DeepCopy() PodResources {
	copiedPodResources := make(PodResources)

	for podUID, containerResources := range pr {
		copiedPodResources[podUID] = containerResources.DeepCopy()
	}

	return copiedPodResources
}

func (cr ContainerResources) DeepCopy() ContainerResources {
	copiedContainerResources := make(ContainerResources)

	for containerName, resouceAllocation := range cr {
		copiedContainerResources[containerName] = resouceAllocation.DeepCopy()
	}

	return copiedContainerResources
}

func (ra ResourceAllocation) DeepCopy() ResourceAllocation {
	copiedResourceAllocation := make(ResourceAllocation)

	for resourceName, allocationInfo := range ra {
		copiedResourceAllocation[resourceName] = proto.Clone(allocationInfo).(*pluginapi.ResourceAllocationInfo)
	}

	return copiedResourceAllocation
}

func (pres *podResourcesChk) pods() sets.String {
	pres.RLock()
	defer pres.RUnlock()

	ret := sets.NewString()
	for k := range pres.resources {
		ret.Insert(k)
	}
	return ret
}

// "resourceName" here is different than "resourceName" in qrm allocation, one qrm plugin may
// only represent one resource in allocation, but can also return several other resourceNames
// to store in pod resources
func (pres *podResourcesChk) insert(podUID, contName, resourceName string, allocationInfo *pluginapi.ResourceAllocationInfo) {
	if allocationInfo == nil {
		return
	}

	pres.Lock()
	defer pres.Unlock()

	if _, podExists := pres.resources[podUID]; !podExists {
		pres.resources[podUID] = make(ContainerResources)
	}
	if _, contExists := pres.resources[podUID][contName]; !contExists {
		pres.resources[podUID][contName] = make(ResourceAllocation)
	}

	pres.resources[podUID][contName][resourceName] = proto.Clone(allocationInfo).(*pluginapi.ResourceAllocationInfo)
}

func (pres *podResourcesChk) delete(pods []string) {
	pres.Lock()
	defer pres.Unlock()

	for _, uid := range pods {
		delete(pres.resources, uid)
	}
}

func (pres *podResourcesChk) podResources(podUID string) ContainerResources {
	pres.RLock()
	defer pres.RUnlock()

	if _, podExists := pres.resources[podUID]; !podExists {
		return nil
	}

	return pres.resources[podUID]
}

// Returns all resources information allocated to the given container.
// Returns nil if we don't have cached state for the given <podUID, contName>.
func (pres *podResourcesChk) containerAllResources(podUID, contName string) ResourceAllocation {
	pres.RLock()
	defer pres.RUnlock()

	if _, podExists := pres.resources[podUID]; !podExists {
		return nil
	}
	if _, contExists := pres.resources[podUID][contName]; !contExists {
		return nil
	}

	return pres.resources[podUID][contName].DeepCopy()
}

// Returns resource information allocated to the given container for the given resource.
// Returns nil if we don't have cached state for the given <podUID, contName, resource>.
func (pres *podResourcesChk) containerResource(podUID, contName, resource string) *pluginapi.ResourceAllocationInfo {
	pres.RLock()
	defer pres.RUnlock()

	if _, podExists := pres.resources[podUID]; !podExists {
		return nil
	}
	if _, contExists := pres.resources[podUID][contName]; !contExists {
		return nil
	}
	resourceAllocationInfo, resourceExists := pres.resources[podUID][contName][resource]
	if !resourceExists {
		return nil
	}
	return proto.Clone(resourceAllocationInfo).(*pluginapi.ResourceAllocationInfo)
}

// Returns allocated scalar resources quantity used to sanitize node allocatable when pod admitting.
// Only for scalar resources need to be updated to node status.
func (pres *podResourcesChk) scalarResourcesQuantity() map[string]float64 {
	pres.RLock()
	defer pres.RUnlock()

	ret := make(map[string]float64)
	for _, containerResources := range pres.resources {
		for _, resourcesAllocation := range containerResources {
			for resourceName, allocationInfo := range resourcesAllocation {
				if allocationInfo.IsNodeResource && allocationInfo.IsScalarResource {
					ret[resourceName] += allocationInfo.AllocatedQuantity
				}
			}
		}
	}
	return ret
}

// Turns podResourcesChk to checkpointData.
func (pres *podResourcesChk) toCheckpointData() []checkpoint.PodResourcesEntry {
	pres.RLock()
	defer pres.RUnlock()

	var data []checkpoint.PodResourcesEntry
	for podUID, containerResources := range pres.resources {
		for conName, resourcesAllocation := range containerResources {
			for resourceName, allocationInfo := range resourcesAllocation {
				allocRespBytes, err := allocationInfo.Marshal()
				if err != nil {
					klog.Errorf("Can't marshal allocationInfo for %v %v %v: %v", podUID, conName, resourceName, err)
					continue
				}
				data = append(data, checkpoint.PodResourcesEntry{
					PodUID:         podUID,
					ContainerName:  conName,
					ResourceName:   resourceName,
					AllocationInfo: string(allocRespBytes)})
			}
		}
	}
	return data
}

// Populates podResourcesChk from the passed in checkpointData.
func (pres *podResourcesChk) fromCheckpointData(data []checkpoint.PodResourcesEntry) {
	for _, entry := range data {
		klog.V(2).Infof("Get checkpoint entry: %s %s %s %s\n",
			entry.PodUID, entry.ContainerName, entry.ResourceName, entry.AllocationInfo)
		allocationInfo := &pluginapi.ResourceAllocationInfo{}
		err := allocationInfo.Unmarshal([]byte(entry.AllocationInfo))
		if err != nil {
			klog.Errorf("Can't unmarshal allocationInfo for %s %s %s %s: %v",
				entry.PodUID, entry.ContainerName, entry.ResourceName, entry.AllocationInfo, err)
			continue
		}
		pres.insert(entry.PodUID, entry.ContainerName, entry.ResourceName, allocationInfo)
	}
}

// [TODO](sunjianyu) to support setting value for struct type recursively
func setReflectValue(valueObj reflect.Value, value string) error {
	switch valueObj.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		valueInt, pErr := strconv.ParseInt(value, 10, 64)

		if pErr != nil {
			return fmt.Errorf("parse: %s to int failed with error: %v", value, pErr)
		}

		valueObj.SetInt(valueInt)
	case reflect.String:
		valueObj.SetString(value)
	default:
		return fmt.Errorf("not supported type: %v set to value: %s", valueObj.Kind(), value)
	}

	return nil
}

func assembleOciResourceConfig(podUID, containerName string, opts *kubecontainer.ResourceRunContainerOptions, resources map[string]*pluginapi.ResourceAllocationInfo) error {
	if opts == nil {
		return fmt.Errorf("assembleOciResourceConfig got nil options")
	}

	ociResourceConfig := &runtimeapi.LinuxContainerResources{}
	ociResourceConfigValueElem := reflect.ValueOf(ociResourceConfig).Elem()

	for resourceName, resourceAllocationInfo := range resources {
		if resourceAllocationInfo == nil {
			klog.Warningf("[qosresourcemanager.assembleOciResourceConfig] resource: %s with nil resourceAllocationInfo", resourceName)
			continue
		}

		if resourceAllocationInfo.OciPropertyName != "" {
			field := ociResourceConfigValueElem.FieldByName(resourceAllocationInfo.OciPropertyName)

			if field == EmptyValue {
				return fmt.Errorf("OCI resource config doesn't support oci property name: %s for resource: %s", resourceAllocationInfo.OciPropertyName, resourceName)
			}

			sErr := setReflectValue(field, resourceAllocationInfo.AllocatationResult)

			if sErr != nil {
				return fmt.Errorf("set oci property name: %s for resource: %s to value: %s failed with error: %v",
					resourceAllocationInfo.OciPropertyName, resourceName, resourceAllocationInfo.AllocatationResult, sErr)
			}

			klog.Infof("[qosresourcemanager.assembleOciResourceConfig] podUID: %s, containerName: %s, set oci property: %s for resource: %s to value: %s in OCI resource config ",
				podUID, containerName, resourceAllocationInfo.OciPropertyName, resourceName, resourceAllocationInfo.AllocatationResult)
		}
	}

	opts.Resources = ociResourceConfig
	return nil
}

func (pres *podResourcesChk) allAllocatedNodeResourceNames() sets.String {
	pres.RLock()
	defer pres.RUnlock()

	res := sets.NewString()

	for _, containerResources := range pres.resources {
		for _, resourcesAllocation := range containerResources {
			for resourceName, allocation := range resourcesAllocation {
				if allocation.IsNodeResource {
					res.Insert(resourceName)
				}
			}
		}
	}

	return res
}

func (pres *podResourcesChk) allAllocatedResourceNames() sets.String {
	pres.RLock()
	defer pres.RUnlock()

	res := sets.NewString()

	for _, containerResources := range pres.resources {
		for _, resourcesAllocation := range containerResources {
			for resourceName := range resourcesAllocation {
				res.Insert(resourceName)
			}
		}
	}

	return res
}

// Returns combined container runtime settings to consume the container's allocated resources.
func (pres *podResourcesChk) resourceRunContainerOptions(podUID, contName string) (*kubecontainer.ResourceRunContainerOptions, error) {
	pres.RLock()
	defer pres.RUnlock()

	containers, exists := pres.resources[podUID]
	if !exists {
		return nil, nil
	}
	resources, exists := containers[contName]
	if !exists {
		return nil, nil
	}
	opts := &kubecontainer.ResourceRunContainerOptions{}

	// Maps to detect duplicate settings.
	envsMap := make(map[string]string)
	annotationsMap := make(map[string]string)
	for _, resourceAllocationInfo := range resources {
		for k, v := range resourceAllocationInfo.Envs {
			if e, ok := envsMap[k]; ok {
				klog.V(4).Infof("[qosresourcemanager] skip existing env %s %s for pod: %s, container: %s", k, v, podUID, contName)
				if e != v {
					klog.Errorf("[qosresourcemanager] environment variable %s has conflicting setting: %s and %s for for pod: %s, container: %s",
						k, e, v, podUID, contName)
				}
				continue
			}
			klog.V(4).Infof("[qosresourcemanager] add env %s %s for pod: %s, container: %s", k, v, podUID, contName)
			envsMap[k] = v
			opts.Envs = append(opts.Envs, kubecontainer.EnvVar{Name: k, Value: v})
		}

		// Updates for Annotations
		for k, v := range resourceAllocationInfo.Annotations {
			if e, ok := annotationsMap[k]; ok {
				klog.V(4).Infof("[qosresourcemanager] skip existing annotation %s %s for pod: %s, container: %s", k, v, podUID, contName)
				if e != v {
					klog.Errorf("[qosresourcemanager] annotation %s has conflicting setting: %s and %s for pod: %s, container: %s", k, e, v, podUID, contName)
				}
				continue
			}
			klog.V(4).Infof("[qosresourcemanager] add annotation %s %s for pod: %s, container: %s", k, v, podUID, contName)
			annotationsMap[k] = v
			opts.Annotations = append(opts.Annotations, kubecontainer.Annotation{Name: k, Value: v})
		}
	}

	err := assembleOciResourceConfig(podUID, contName, opts, resources)

	if err != nil {
		return nil, fmt.Errorf("assembleOciResourceConfig failed with error: %v", err)
	}

	return opts, nil
}
