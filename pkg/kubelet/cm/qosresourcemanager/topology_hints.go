/*
Copyright 2019 The Kubernetes Authors.

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
	"context"
	"math"
	"time"

	"google.golang.org/grpc/metadata"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/metrics"
)

func (m *ManagerImpl) GetPodTopologyHints(pod *v1.Pod) map[string][]topologymanager.TopologyHint {
	// [TODO] need to implement when apply pod scode affinity for qos resource manager
	return nil
}

// GetTopologyHints implements the TopologyManager HintProvider Interface which
// ensures the Resource Manager is consulted when Topology Aware Hints for each
// container are created.
func (m *ManagerImpl) GetTopologyHints(pod *v1.Pod, container *v1.Container) map[string][]topologymanager.TopologyHint {
	// Garbage collect any stranded resource resources before providing TopologyHints
	m.UpdateAllocatedResources()

	// Loop through all resources and generate TopologyHints for them.
	resourceHints := make(map[string][]topologymanager.TopologyHint)
	for resourceObj, requestedObj := range container.Resources.Requests {
		resource := string(resourceObj)
		requested := int(requestedObj.Value())

		// Only consider resources associated with a resource plugin.
		if m.isResourcePluginResource(resource) && !requestedObj.IsZero() {
			// Only consider resources that are actually with topology alignment
			if aligned := m.resourceHasTopologyAlignment(resource); !aligned {
				klog.Infof("[qosresourcemanager] Resource '%v' does not have a topology preference", resource)
				resourceHints[resource] = nil
				continue
			}

			// Short circuit to regenerate the same hints if there are already
			// resources allocated to the Container. This might happen after a
			// kubelet restart, for example.
			allocationInfo := m.podResources.containerResource(string(pod.UID), container.Name, resource)
			if allocationInfo.ResourceHints != nil && len(allocationInfo.ResourceHints.Hints) > 0 {
				if allocationInfo.IsScalarResource && int(math.Ceil(allocationInfo.AllocatedQuantity)) >= requested {
					resourceHints[resource] = ParseListOfTopologyHints(allocationInfo.ResourceHints)
					continue
				} else {
					klog.Warningf("[qosresourcemanager] Resource '%s' already allocated to (pod %s/%s, container %v) with smaller number than request: requested: %d, allocated: %d; continue to getTopologyHints",
						resource, pod.GetNamespace(), pod.GetName(), container.Name, requested, int(math.Ceil(allocationInfo.AllocatedQuantity)))
				}
			}

			startRPCTime := time.Now()
			m.mutex.Lock()
			eI, ok := m.endpoints[resource]
			m.mutex.Unlock()
			if !ok {
				klog.Errorf("[qosresourcemanager] unknown Resource Plugin %s", resource)
				continue
			}

			klog.V(3).Infof("Making GetTopologyHints request of resources %s for pod: %s/%s; container: %s",
				resource, pod.Namespace, pod.Name, container.Name)

			resourceReq := &pluginapi.ResourceRequest{
				PodUid:           string(pod.GetUID()),
				PodNamespace:     pod.GetNamespace(),
				PodName:          pod.GetName(),
				ContainerName:    container.Name,
				IsInitContainer:  IsInitContainerOfPod(pod, container),
				PodRole:          pod.Labels[pluginapi.PodRoleLabelKey],
				PodType:          pod.Annotations[pluginapi.PodTypeAnnoatationKey],
				ResourceName:     resource,
				ResourceRequests: map[string]float64{resource: ParseQuantityToFloat64(requestedObj)},
			}

			ctx := metadata.NewOutgoingContext(context.Background(), metadata.New(nil))
			resp, err := eI.e.getTopologyHints(ctx, resourceReq)
			metrics.ResourcePluginGetTopologyHintsDuration.WithLabelValues(resource).Observe(metrics.SinceInSeconds(startRPCTime))
			if err != nil {
				klog.Errorf("[qosresourcemanager] call GetTopologyHints of %s resource plugin for pod: %s/%s, container: %s failed with error: %v",
					resource, pod.GetNamespace(), pod.GetName(), container.Name, err)

				// empty TopologyHint list will cause fail in restricted topology manager policy
				// nil TopologyHint list assumes no NUMA preference
				resourceHints[resource] = []topologymanager.TopologyHint{}
				continue
			}

			// think about a resource name with accompanying resources,
			// we must return union result of all accompanying resources in the resource name
			resourceHints[resource] = ParseListOfTopologyHints(resp.ResourceHints[resource])
		}
	}

	return resourceHints
}

func (m *ManagerImpl) resourceHasTopologyAlignment(resource string) bool {
	m.mutex.Lock()
	eI, ok := m.endpoints[resource]
	if !ok {
		m.mutex.Unlock()
		return false
	}

	if eI.opts == nil || !eI.opts.WithTopologyAlignment {
		m.mutex.Unlock()
		klog.V(4).Infof("[qosresourcemanager] resource plugin options indicates that resource: %s without topology alignment", resource)
		return false
	}

	return true
}
