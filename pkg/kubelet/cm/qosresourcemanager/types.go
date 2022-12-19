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
	"time"

	v1 "k8s.io/api/core/v1"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/config"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/pluginmanager/cache"
	"k8s.io/kubernetes/pkg/kubelet/status"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"
)

// ActivePodsFunc is a function that returns a list of pods to reconcile.
type ActivePodsFunc func() []*v1.Pod

type runtimeService interface {
	UpdateContainerResources(id string, resources *runtimeapi.LinuxContainerResources) error
}

// Manager manages all the Resource Plugins running on a node.
type Manager interface {
	// Start starts resource plugin registration service.
	Start(activePods ActivePodsFunc, sourcesReady config.SourcesReady, podstatusprovider status.PodStatusProvider, containerRuntime runtimeService) error

	// Allocate configures and assigns resources to a container in a pod. From
	// the requested resource resources, Allocate will communicate with the
	// owning resource plugin to allow setup procedures to take place, and for
	// the resource plugin to provide runtime settings to use the resource
	// (oci supported resource results, environment variables, annotations, ...).
	Allocate(pod *v1.Pod, container *v1.Container) error

	// UpdatePluginResources updates node resources based on resources already
	// allocated to pods. The node object is provided for the resource manager to
	// update the node capacity to reflect the currently available resources.
	UpdatePluginResources(node *schedulerframework.NodeInfo, attrs *lifecycle.PodAdmitAttributes) error

	// Stop stops the manager.
	Stop() error

	// GetCapacity returns the amount of available resource plugin resource capacity, resource allocatable
	// and inactive resource plugin resources previously registered on the node.
	GetCapacity() (v1.ResourceList, v1.ResourceList, []string)

	// UpdateAllocatedResources frees any resources that are bound to terminated pods.
	UpdateAllocatedResources()

	// TopologyManager HintProvider provider indicates the Resource Manager implements the Topology Manager Interface
	// and is consulted to make Topology aware resource alignments in container scope.
	GetTopologyHints(pod *v1.Pod, container *v1.Container) map[string][]topologymanager.TopologyHint

	// TopologyManager HintProvider provider indicates the Resource Manager implements the Topology Manager Interface
	// and is consulted to make Topology aware resource alignments in pod scope.
	GetPodTopologyHints(pod *v1.Pod) map[string][]topologymanager.TopologyHint

	// GetResourceRunContainerOptions checks whether we have cached containerResources
	// for the passed-in <pod, container> and returns its ResourceRunContainerOptions
	// for the found one. An empty struct is returned in case no cached state is found.
	GetResourceRunContainerOptions(pod *v1.Pod, container *v1.Container) (*kubecontainer.ResourceRunContainerOptions, error)

	// GetTopologyAwareResources returns information about the resources assigned to pods and containers
	// and organized as topology aware format.
	GetTopologyAwareResources(pod *v1.Pod, container *v1.Container) (*pluginapi.GetTopologyAwareResourcesResponse, error)

	// GetTopologyAwareAllocatableResources returns information about the resources assigned to pods and containers
	// and organized as topology aware format.
	GetTopologyAwareAllocatableResources() (*pluginapi.GetTopologyAwareAllocatableResourcesResponse, error)

	// ShouldResetExtendedResourceCapacity returns whether the extended resources should be reset or not,
	// depending on the checkpoint file availability. Absence of the checkpoint file strongly indicates
	// the node has been recreated.
	ShouldResetExtendedResourceCapacity() bool

	// support probe based plugin discovery mechanism in qos resource manager
	GetWatcherHandler() cache.PluginHandler
}

// TODO: evaluate whether we need these error definitions.
const (
	// errFailedToDialResourcePlugin is the error raised when the resource plugin could not be
	// reached on the registered socket
	errFailedToDialResourcePlugin = "failed to dial resource plugin:"
	// errUnsupportedVersion is the error raised when the resource plugin uses an API version not
	// supported by the Kubelet registry
	errUnsupportedVersion = "requested API version %q is not supported by kubelet. Supported version is %q"
	// errInvalidResourceName is the error raised when a resource plugin is registering
	// itself with an invalid ResourceName
	errInvalidResourceName = "the ResourceName %q is invalid"
	// errEndpointStopped indicates that the endpoint has been stopped
	errEndpointStopped = "endpoint %v has been stopped"
	// errBadSocket is the error raised when the registry socket path is not absolute
	errBadSocket = "bad socketPath, must be an absolute path:"
	// errListenSocket is the error raised when the registry could not listen on the socket
	errListenSocket = "failed to listen to socket while starting resource plugin registry, with error"
)

// endpointStopGracePeriod indicates the grace period after an endpoint is stopped
// because its resource plugin fails. QoSResourceManager keeps the stopped endpoint in its
// cache during this grace period to cover the time gap for the capacity change to
// take effect.
const endpointStopGracePeriod = time.Duration(5) * time.Minute

// kubeletQoSResourceManagerCheckpoint is the file name of resource plugin checkpoint
const kubeletQoSResourceManagerCheckpoint = "kubelet_qrm_checkpoint"
