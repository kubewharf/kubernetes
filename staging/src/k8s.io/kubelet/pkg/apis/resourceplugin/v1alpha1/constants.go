/*
Copyright 2018 The Kubernetes Authors.

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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	// Healthy means that the resource is healthy
	Healthy = "Healthy"
	// UnHealthy means that the resource is unhealthy
	Unhealthy = "Unhealthy"

	// Current version of the API supported by kubelet
	Version = "v1alpha1"
	// ResourcePluginPath is the folder the Resource Plugin is expecting sockets to be on
	// Only privileged pods have access to this path
	// Note: Placeholder until we find a "standard path"
	ResourcePluginPath = "/var/lib/kubelet/resource-plugins/"
	// KubeletSocket is the path of the Kubelet registry socket
	KubeletSocket = ResourcePluginPath + "kubelet.sock"
	// Timeout duration in secs for PreStartContainer RPC
	KubeletResourcePluginPreStartContainerRPCTimeoutInSecs = 30
	// Timeout duration in secs for Allocate RPC
	KubeletResourcePluginAllocateRPCTimeoutInSecs = 10
	// Timeout duration in secs for GetTopologyHints RPC
	KubeletResourcePluginGetTopologyHintsRPCTimeoutInSecs = 10
	// Timeout duration in secs for RemovePod RPC
	KubeletResourcePluginRemovePodRPCTimeoutInSecs = 10
	// Timeout duration in secs for GetResourcesAllocation RPC
	KubeletResourcePluginGetResourcesAllocationRPCTimeoutInSecs = 10
	// Timeout duration in secs for GetTopologyAwareResources RPC
	KubeletResourcePluginGetTopologyAwareResourcesRPCTimeoutInSecs = 10
	// Timeout duration in secs for GetTopologyAwareAllocatableResources RPC
	KubeletResourcePluginGetTopologyAwareAllocatableResourcesRPCTimeoutInSecs = 10

	PodRoleLabelKey      = "katalyst.kubewharf.io/pod_role"
	PodTypeAnnotationKey = "katalyst.kubewharf.io/pod_type"

	KatalystQoSLevelAnnotationKey     = "katalyst.kubewharf.io/qos_level"
	KatalystNumaBindingAnnotationKey  = "katalyst.kubewharf.io/numa_binding"
	KatalystSkipQRMAdmitAnnotationKey = "katalyst.kubewharf.io/skip_qrm_admit"

	KatalystQoSLevelLabelKey = KatalystQoSLevelAnnotationKey

	KatalystQoSLevelDedicatedCores = "dedicated_cores"
	KatalystQoSLevelSharedCores    = "shared_cores"
	KatalystQoSLevelReclaimedCores = "reclaimed_cores"
	KatalystQoSLevelSystemCores    = "system_cores"

	KatalystValueTrue = "true"

	KatalystMemoryEnhancementAnnotationKey = "katalyst.kubewharf.io/memory_enhancement"
	KatalystCPUEnhancementAnnotationKey    = "katalyst.kubewharf.io/cpu_enhancement"

	KatalystMemoryEnhancementKeyNumaBinding = "numa_binding"
)

var SupportedVersions = [...]string{"v1alpha1"}
var SupportedKatalystQoSLevels = sets.NewString(
	KatalystQoSLevelDedicatedCores,
	KatalystQoSLevelSharedCores,
	KatalystQoSLevelReclaimedCores,
	KatalystQoSLevelSystemCores,
)
