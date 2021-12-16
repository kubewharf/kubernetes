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
	// TODO: Migrate kubelet to either use its own internal objects or client library.
	v1 "k8s.io/api/core/v1"
	internalapi "k8s.io/cri-api/pkg/apis"
	podresourcesapi "k8s.io/kubelet/pkg/apis/podresources/v1"
	resourcepluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
	"k8s.io/kubernetes/pkg/kubelet/apis/podresources"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"k8s.io/kubernetes/pkg/kubelet/cm/devicemanager"
	"k8s.io/kubernetes/pkg/kubelet/config"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	evictionapi "k8s.io/kubernetes/pkg/kubelet/eviction/api"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/pluginmanager/cache"
	"k8s.io/kubernetes/pkg/kubelet/status"
	schedulernodeinfo "k8s.io/kubernetes/pkg/scheduler/nodeinfo"
)

type ActivePodsFunc func() []*v1.Pod

// Manages the containers running on a machine.
type ContainerManager interface {
	// Runs the container manager's housekeeping.
	// - Ensures that the Docker daemon is in a container.
	// - Creates the system container where all non-containerized processes run.
	Start(*v1.Node, ActivePodsFunc, ActivePodsFunc, config.SourcesReady, status.PodStatusProvider, internalapi.RuntimeService) error

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

	GetDevicePluginRefinedResource() devicemanager.DevicePluginHeterogenousResource

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
	UpdatePluginResources(*schedulernodeinfo.NodeInfo, *lifecycle.PodAdmitAttributes) error

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

	// GetResources returns ResourceRunContainerOptions with OCI resources config, annotations and envs fields populated for
	// resources are managed by qos resource manager and required by container.
	GetResourceRunContainerOptions(pod *v1.Pod, container *v1.Container) (*kubecontainer.ResourceRunContainerOptions, error)

	// Implements the podresources Provider API for CPUs, Devices and Resources
	podresources.CPUsProvider
	podresources.DevicesProvider
	podresources.ResourcesProvider
}

type NodeConfig struct {
	RuntimeCgroupsName    string
	SystemCgroupsName     string
	KubeletCgroupsName    string
	ContainerRuntime      string
	CgroupsPerQOS         bool
	CgroupRoot            string
	CgroupDriver          string
	KubeletRootDir        string
	ProtectKernelDefaults bool
	NodeAllocatableConfig
	QOSReserved                                   map[v1.ResourceName]int64
	ExperimentalCPUManagerPolicy                  string
	ExperimentalCPUManagerReconcilePeriod         time.Duration
	ExperimentalQoSResourceManagerReconcilePeriod time.Duration
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

func convertTopologyAwareResourceToPodResourceApi(topologyAwareResources map[string]*resourcepluginapi.TopologyAwareResource) []*podresourcesapi.TopologyAwareResource {
	result := make([]*podresourcesapi.TopologyAwareResource, 0, len(topologyAwareResources))

	for resourceName, resource := range topologyAwareResources {
		if resource == nil {
			continue
		}

		topologyAwareQuantityList := make([]*podresourcesapi.TopologyAwareQuantity, 0, len(resource.TopologyAwareQuantityList))

		for _, topologyAwareQuantity := range resource.TopologyAwareQuantityList {
			if topologyAwareQuantity != nil {
				topologyAwareQuantityList = append(topologyAwareQuantityList, &podresourcesapi.TopologyAwareQuantity{
					ResourceValue: topologyAwareQuantity.ResourceValue,
					Node:          topologyAwareQuantity.Node,
				})
			}
		}

		result = append(result, &podresourcesapi.TopologyAwareResource{
			ResourceName:              resourceName,
			IsNodeResource:            resource.IsNodeResource,
			IsScalarResource:          resource.IsScalarResource,
			AggregatedQuantity:        resource.AggregatedQuantity,
			TopologyAwareQuantityList: topologyAwareQuantityList,
		})
	}

	return result
}

func containerResourcesFromResourceManagerAllocatableResponse(res *resourcepluginapi.GetTopologyAwareAllocatableResourcesResponse) []*podresourcesapi.TopologyAwareResource {
	if res == nil || res.AllocatableResources == nil {
		return nil
	}

	return convertTopologyAwareResourceToPodResourceApi(res.AllocatableResources.TopologyAwareResources)
}

func containerResourcesFromResourceManagerResponse(res *resourcepluginapi.GetTopologyAwareResourcesResponse) []*podresourcesapi.TopologyAwareResource {
	if res == nil ||
		res.ContainerTopologyAwareResources == nil ||
		res.ContainerTopologyAwareResources.AllocatedResources == nil {
		return nil
	}

	return convertTopologyAwareResourceToPodResourceApi(res.ContainerTopologyAwareResources.AllocatedResources.TopologyAwareResources)
}

func containerDevicesFromResourceDeviceInstances(devs devicemanager.ResourceDeviceInstances) []*podresourcesapi.ContainerDevices {
	var respDevs []*podresourcesapi.ContainerDevices

	for resourceName, resourceDevs := range devs {
		for devID, dev := range resourceDevs {
			for _, node := range dev.GetTopology().GetNodes() {
				numaNode := node.GetID()
				respDevs = append(respDevs, &podresourcesapi.ContainerDevices{
					ResourceName: resourceName,
					DeviceIds:    []string{devID},
					Topology: &podresourcesapi.TopologyInfo{
						Nodes: []*podresourcesapi.NUMANode{
							{
								ID: numaNode,
							},
						},
					},
				})
			}
		}
	}

	return respDevs
}
