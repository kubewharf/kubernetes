/*
Copyright 2015 The Kubernetes Authors.

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

package cm

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	// TODO: Migrate kubelet to either use its own internal objects or client library.
	v1 "k8s.io/api/core/v1"
	internalapi "k8s.io/cri-api/pkg/apis"
	podresourcesapi "k8s.io/kubelet/pkg/apis/podresources/v1"
	resourcepluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
	kubeletconfig "k8s.io/kubernetes/pkg/kubelet/apis/config"
	"k8s.io/kubernetes/pkg/kubelet/apis/podresources"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"k8s.io/kubernetes/pkg/kubelet/cm/devicemanager"
	"k8s.io/kubernetes/pkg/kubelet/config"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	evictionapi "k8s.io/kubernetes/pkg/kubelet/eviction/api"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/pluginmanager/cache"
	"k8s.io/kubernetes/pkg/kubelet/status"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"
	maputil "k8s.io/kubernetes/pkg/util/maps"
)

type ActivePodsFunc func() []*v1.Pod

// Manages the containers running on a machine.
type ContainerManager interface {
	// Runs the container manager's housekeeping.
	// - Ensures that the Docker daemon is in a container.
	// - Creates the system container where all non-containerized processes run.
	Start(*v1.Node, ActivePodsFunc, config.SourcesReady, status.PodStatusProvider, internalapi.RuntimeService) error

	// SystemCgroupsLimit returns resources allocated to system cgroups in the machine.
	// These cgroups include the system and Kubernetes services.
	SystemCgroupsLimit() v1.ResourceList

	// GetNodeConfig returns a NodeConfig that is being used by the container manager.
	GetNodeConfig() NodeConfig

	// Status returns internal Status.
	Status() Status

	// NewPodContainerManager is a factory method which returns a podContainerManager object
	// Returns a noop implementation if qos cgroup hierarchy is not enabled
	NewPodContainerManager() PodContainerManager

	// GetMountedSubsystems returns the mounted cgroup subsystems on the node
	GetMountedSubsystems() *CgroupSubsystems

	// GetQOSContainersInfo returns the names of top level QoS containers
	GetQOSContainersInfo() QOSContainersInfo

	// GetNodeAllocatableReservation returns the amount of compute resources that have to be reserved from scheduling.
	GetNodeAllocatableReservation() v1.ResourceList

	// GetCapacity returns the amount of compute resources tracked by container manager available on the node.
	GetCapacity() v1.ResourceList

	// GetResourcePluginResourceCapacity returns the node capacity (amount of total resource plugin resources),
	// node allocatable (amount of total healthy resources reported by resource plugin),
	// and inactive resource plugin resources previously registered on the node.
	// notice: only resources with IsNodeResource: True and IsScalarResource: True will be reported by this function.
	GetResourcePluginResourceCapacity() (v1.ResourceList, v1.ResourceList, []string)

	// GetDevicePluginResourceCapacity returns the node capacity (amount of total device plugin resources),
	// node allocatable (amount of total healthy resources reported by device plugin),
	// and inactive device plugin resources previously registered on the node.
	GetDevicePluginResourceCapacity() (v1.ResourceList, v1.ResourceList, []string)

	// UpdateQOSCgroups performs housekeeping updates to ensure that the top
	// level QoS containers have their desired state in a thread-safe way
	UpdateQOSCgroups() error

	// GetResources returns RunContainerOptions with devices, mounts, and env fields populated for
	// extended resources required by container.
	GetResources(pod *v1.Pod, container *v1.Container) (*kubecontainer.RunContainerOptions, error)

	// UpdatePluginResources calls Allocate of device plugin handler for potential
	// requests for device plugin resources, and returns an error if fails.
	// Otherwise, it updates allocatableResource in nodeInfo if necessary,
	// to make sure it is at least equal to the pod's requested capacity for
	// any registered device plugin resource
	UpdatePluginResources(*schedulerframework.NodeInfo, *lifecycle.PodAdmitAttributes) error

	InternalContainerLifecycle() InternalContainerLifecycle

	// GetPodCgroupRoot returns the cgroup which contains all pods.
	GetPodCgroupRoot() string

	// GetPluginRegistrationHandler returns a plugin registration handler
	// The pluginwatcher's Handlers allow to have a single module for handling
	// registration.
	GetPluginRegistrationHandler() map[string]cache.PluginHandler

	// ShouldResetExtendedResourceCapacity returns whether or not the extended resources should be zeroed,
	// due to node recreation.
	ShouldResetExtendedResourceCapacity() bool

	// GetAllocateResourcesPodAdmitHandler returns an instance of a PodAdmitHandler responsible for allocating pod resources.
	GetAllocateResourcesPodAdmitHandler() lifecycle.PodAdmitHandler

	// GetNodeAllocatableAbsolute returns the absolute value of Node Allocatable which is primarily useful for enforcement.
	GetNodeAllocatableAbsolute() v1.ResourceList

	// GetResources returns ResourceRunContainerOptions with OCI resources config, annotations and envs fields populated for
	// resources are managed by qos resource manager and required by container.
	GetResourceRunContainerOptions(pod *v1.Pod, container *v1.Container) (*kubecontainer.ResourceRunContainerOptions, error)

	// Implements the podresources Provider API for CPUs, Memory and Devices
	podresources.CPUsProvider
	podresources.DevicesProvider
	podresources.MemoryProvider
	podresources.ResourcesProvider
}

type NodeConfig struct {
	RuntimeCgroupsName    string
	SystemCgroupsName     string
	KubeletCgroupsName    string
	KubeletOOMScoreAdj    int32
	ContainerRuntime      string
	CgroupsPerQOS         bool
	CgroupRoot            string
	CgroupDriver          string
	KubeletRootDir        string
	ProtectKernelDefaults bool
	NodeAllocatableConfig
	QOSReserved                                   map[v1.ResourceName]int64
	ExperimentalCPUManagerPolicy                  string
	ExperimentalCPUManagerPolicyOptions           map[string]string
	ExperimentalTopologyManagerScope              string
	ExperimentalCPUManagerReconcilePeriod         time.Duration
	ExperimentalMemoryManagerPolicy               string
	ExperimentalMemoryManagerReservedMemory       []kubeletconfig.MemoryReservation
	ExperimentalQoSResourceManagerReconcilePeriod time.Duration
	QoSResourceManagerResourceNamesMap            map[string]string
	ExperimentalPodPidsLimit                      int64
	EnforceCPULimits                              bool
	CPUCFSQuotaPeriod                             time.Duration
	ExperimentalTopologyManagerPolicy             string
}

type NodeAllocatableConfig struct {
	KubeReservedCgroupName   string
	SystemReservedCgroupName string
	ReservedSystemCPUs       cpuset.CPUSet
	EnforceNodeAllocatable   sets.String
	KubeReserved             v1.ResourceList
	SystemReserved           v1.ResourceList
	HardEvictionThresholds   []evictionapi.Threshold
}

type Status struct {
	// Any soft requirements that were unsatisfied.
	SoftRequirements error
}

// parsePercentage parses the percentage string to numeric value.
func parsePercentage(v string) (int64, error) {
	if !strings.HasSuffix(v, "%") {
		return 0, fmt.Errorf("percentage expected, got '%s'", v)
	}
	percentage, err := strconv.ParseInt(strings.TrimRight(v, "%"), 10, 0)
	if err != nil {
		return 0, fmt.Errorf("invalid number in percentage '%s'", v)
	}
	if percentage < 0 || percentage > 100 {
		return 0, fmt.Errorf("percentage must be between 0 and 100")
	}
	return percentage, nil
}

// ParseQOSReserved parses the --qos-reserve-requests option
func ParseQOSReserved(m map[string]string) (*map[v1.ResourceName]int64, error) {
	reservations := make(map[v1.ResourceName]int64)
	for k, v := range m {
		switch v1.ResourceName(k) {
		// Only memory resources are supported.
		case v1.ResourceMemory:
			q, err := parsePercentage(v)
			if err != nil {
				return nil, err
			}
			reservations[v1.ResourceName(k)] = q
		default:
			return nil, fmt.Errorf("cannot reserve %q resource", k)
		}
	}
	return &reservations, nil
}

func containerResourcesFromResourceManagerAllocatableResponse(res *resourcepluginapi.GetTopologyAwareAllocatableResourcesResponse) []*podresourcesapi.AllocatableTopologyAwareResource {
	if res == nil {
		return nil
	}

	result := make([]*podresourcesapi.AllocatableTopologyAwareResource, 0, len(res.AllocatableResources))

	for resourceName, resource := range res.AllocatableResources {
		if resource == nil {
			continue
		}

		result = append(result, &podresourcesapi.AllocatableTopologyAwareResource{
			ResourceName:                         resourceName,
			IsNodeResource:                       resource.IsNodeResource,
			IsScalarResource:                     resource.IsScalarResource,
			AggregatedAllocatableQuantity:        resource.AggregatedAllocatableQuantity,
			TopologyAwareAllocatableQuantityList: transformTopologyAwareQuantity(resource.TopologyAwareAllocatableQuantityList),
			AggregatedCapacityQuantity:           resource.AggregatedCapacityQuantity,
			TopologyAwareCapacityQuantityList:    transformTopologyAwareQuantity(resource.TopologyAwareCapacityQuantityList),
		})
	}

	return result
}

func containerResourcesFromResourceManagerResponse(res *resourcepluginapi.GetTopologyAwareResourcesResponse) []*podresourcesapi.TopologyAwareResource {
	if res == nil ||
		res.ContainerTopologyAwareResources == nil {
		return nil
	}

	result := make([]*podresourcesapi.TopologyAwareResource, 0, len(res.ContainerTopologyAwareResources.AllocatedResources))

	for resourceName, resource := range res.ContainerTopologyAwareResources.AllocatedResources {
		if resource == nil {
			continue
		}

		result = append(result, &podresourcesapi.TopologyAwareResource{
			ResourceName:                      resourceName,
			IsNodeResource:                    resource.IsNodeResource,
			IsScalarResource:                  resource.IsScalarResource,
			AggregatedQuantity:                resource.AggregatedQuantity,
			OriginalAggregatedQuantity:        resource.OriginalAggregatedQuantity,
			TopologyAwareQuantityList:         transformTopologyAwareQuantity(resource.TopologyAwareQuantityList),
			OriginalTopologyAwareQuantityList: transformTopologyAwareQuantity(resource.OriginalTopologyAwareQuantityList),
		})
	}

	return result
}

func transformTopologyAwareQuantity(pluginAPITopologyAwareQuantityList []*resourcepluginapi.TopologyAwareQuantity) []*podresourcesapi.TopologyAwareQuantity {
	if pluginAPITopologyAwareQuantityList == nil {
		return nil
	}

	topologyAwareQuantityList := make([]*podresourcesapi.TopologyAwareQuantity, 0, len(pluginAPITopologyAwareQuantityList))

	for _, topologyAwareQuantity := range pluginAPITopologyAwareQuantityList {
		if topologyAwareQuantity != nil {
			topologyAwareQuantityList = append(topologyAwareQuantityList, &podresourcesapi.TopologyAwareQuantity{
				ResourceValue: topologyAwareQuantity.ResourceValue,
				Node:          topologyAwareQuantity.Node,
				Name:          topologyAwareQuantity.Name,
				Type:          topologyAwareQuantity.Type,
				TopologyLevel: transformTopologyLevel(topologyAwareQuantity.TopologyLevel),
				Annotations:   maputil.CopySS(topologyAwareQuantity.Annotations),
			})
		}
	}

	return topologyAwareQuantityList
}

func transformTopologyLevel(pluginAPITopologyLevel resourcepluginapi.TopologyLevel) podresourcesapi.TopologyLevel {
	switch pluginAPITopologyLevel {
	case resourcepluginapi.TopologyLevel_NUMA:
		return podresourcesapi.TopologyLevel_NUMA
	case resourcepluginapi.TopologyLevel_SOCKET:
		return podresourcesapi.TopologyLevel_SOCKET
	}

	klog.Warningf("[transformTopologyLevel] unrecognized pluginAPITopologyLevel %s:%v, set podResouresAPITopologyLevel to default value: %s:%v",
		pluginAPITopologyLevel.String(), pluginAPITopologyLevel, podresourcesapi.TopologyLevel_NUMA.String(), podresourcesapi.TopologyLevel_NUMA)
	return podresourcesapi.TopologyLevel_NUMA
}

func containerDevicesFromResourceDeviceInstances(devs devicemanager.ResourceDeviceInstances) []*podresourcesapi.ContainerDevices {
	var respDevs []*podresourcesapi.ContainerDevices

	for resourceName, resourceDevs := range devs {
		for devID, dev := range resourceDevs {
			topo := dev.GetTopology()
			if topo == nil {
				// Some device plugin do not report the topology information.
				// This is legal, so we report the devices anyway,
				// let the client decide what to do.
				respDevs = append(respDevs, &podresourcesapi.ContainerDevices{
					ResourceName: resourceName,
					DeviceIds:    []string{devID},
				})
				continue
			}

			for _, node := range topo.GetNodes() {
				respDevs = append(respDevs, &podresourcesapi.ContainerDevices{
					ResourceName: resourceName,
					DeviceIds:    []string{devID},
					Topology: &podresourcesapi.TopologyInfo{
						Nodes: []*podresourcesapi.NUMANode{
							{
								ID: node.GetID(),
							},
						},
					},
				})
			}
		}
	}

	return respDevs
}
