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

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/metrics"
	maputil "k8s.io/kubernetes/pkg/util/maps"
)

// Todo(gaohuatao): implement this function for VCI pod and move it to the right place
func GetPodResources(pod *v1.Pod) v1.ResourceRequirements {
	return v1.ResourceRequirements{}
}

func (m *ManagerImpl) GetPodTopologyHints(pod *v1.Pod) map[string][]topologymanager.TopologyHint {
	if pod == nil {
		klog.Errorf("[qosresourcemanager] GetPodTopologyHints got nil pod: %v", pod)
		return nil
	}

	if isSkippedPod(pod, true, PodHasAnyTargetAnnoKey(pod, m.podSkipAnnotationKeys)) {
		klog.V(4).Infof("[qosresourcemanager] skip get topology hints for pod")
		return nil
	}

	// Garbage collect any stranded resource resources before providing TopologyHints
	m.UpdateAllocatedResources()

	// Loop through all resources and generate TopologyHints for them.
	resourceHints := make(map[string][]topologymanager.TopologyHint)
	resourceList := m.getResourcesFromPod(pod).Limits
	for resourceObj, requestedObj := range resourceList {
		reqResource := string(resourceObj)
		requested := int(requestedObj.Value())

		resource, err := m.getMappedResourceName(reqResource, resourceList)
		if err != nil {
			klog.Errorf("[qosresourcemanager] getMappedResourceName failed with error: %v", err)

			// empty TopologyHint list will cause fail in restricted topology manager policy
			// nil TopologyHint list assumes no NUMA preference
			resourceHints[reqResource] = []topologymanager.TopologyHint{}
			continue
		}

		klog.Infof("[qosresourcemanager] pod: %s/%s needs %d %s, to get topology hint",
			pod.Namespace, pod.Name, requested, resource)

		// Only consider resources associated with a resource plugin.
		if !m.isResourcePluginResource(resource) {
			continue
		}
		// Only consider resources that are actually with topology alignment
		if aligned := m.resourceHasTopologyAlignment(resource); !aligned {
			klog.Infof("[qosresourcemanager] resource '%v' does not have a topology preference", resource)
			resourceHints[resource] = nil
			continue
		}

		// Short circuit to regenerate the same hints if there are already
		// resources allocated to the pod. This might happen after a
		// kubelet restart, for example.
		allocationInfo := m.podResources.containerResource(string(pod.UID), string(pod.UID), resource)
		if allocationInfo != nil && allocationInfo.ResourceHints != nil && len(allocationInfo.ResourceHints.Hints) > 0 {

			allocated := int(math.Ceil(allocationInfo.AllocatedQuantity))

			if allocationInfo.IsScalarResource && allocated >= requested {
				resourceHints[resource] = ParseListOfTopologyHints(allocationInfo.ResourceHints)
				klog.Warningf("[qosresourcemanager] resource %s already allocated to (pod %s/%s) with larger number than request: requested: %d, allocated: %d; not to getTopologyHints",
					resource, pod.GetNamespace(), pod.GetName(), requested, allocated)
				continue
			} else {
				klog.Warningf("[qosresourcemanager] resource %s already allocated to (pod %s/%s) with smaller number than request: requested: %d, allocated: %d; continue to getTopologyHints",
					resource, pod.GetNamespace(), pod.GetName(), requested, int(math.Ceil(allocationInfo.AllocatedQuantity)))
			}
		}

		startRPCTime := time.Now()
		m.mutex.Lock()
		eI, ok := m.endpoints[reqResource]
		m.mutex.Unlock()
		if !ok {
			klog.Errorf("[qosresourcemanager] unknown Resource Plugin %s", resource)

			// empty TopologyHint list will cause fail in restricted topology manager policy
			// nil TopologyHint list assumes no NUMA preference
			resourceHints[reqResource] = []topologymanager.TopologyHint{}
			continue
		}

		klog.Infof("[qosresourcemanager] making GetTopologyHints request of %.3f resources %s for pod: %s/%s",
			ParseQuantityToFloat64(requestedObj), resource, pod.Namespace, pod.Name)

		resourceReq := &pluginapi.PodResourceRequest{
			PodUid:       string(pod.GetUID()),
			PodNamespace: pod.GetNamespace(),
			PodName:      pod.GetName(),
			PodRole:      pod.Labels[pluginapi.PodRoleLabelKey],
			PodType:      pod.Annotations[pluginapi.PodTypeAnnotationKey],
			Labels:       maputil.CopySS(pod.Labels),
			Annotations:  maputil.CopySS(pod.Annotations),
			// use mapped resource name in "ResourceName" to indicates which endpoint to request
			ResourceName: resource,
			// use original requested resource name in "ResourceRequests" in order to make plugin identity real requested resource name
			ResourceRequests: map[string]float64{reqResource: ParseQuantityToFloat64(requestedObj)},
		}

		resp, err := eI.e.getPodTopologyHints(context.Background(), resourceReq)
		metrics.ResourcePluginGetTopologyHintsDuration.WithLabelValues(resource).Observe(metrics.SinceInSeconds(startRPCTime))
		if err != nil {
			klog.Errorf("[qosresourcemanager] call GetTopologyHints of %s resource plugin for pod: %s/%s, failed with error: %v",
				resource, pod.GetNamespace(), pod.GetName(), err)

			// empty TopologyHint list will cause fail in restricted topology manager policy
			// nil TopologyHint list assumes no NUMA preference
			resourceHints[resource] = []topologymanager.TopologyHint{}
			continue
		}

		// think about a resource name with accompanying resources,
		// we must return union result of all accompanying resources in the resource name
		resourceHints[resource] = ParseListOfTopologyHints(resp.ResourceHints[resource])

		klog.Infof("[qosresourcemanager] GetTopologyHints for resource: %s, pod: %s/%s, result: %+v",
			resource, pod.Namespace, pod.Name, resourceHints[resource])
	}

	return resourceHints
}

// GetTopologyHints implements the TopologyManager HintProvider Interface which
// ensures the Resource Manager is consulted when Topology Aware Hints for each
// container are created.
func (m *ManagerImpl) GetTopologyHints(pod *v1.Pod, container *v1.Container) map[string][]topologymanager.TopologyHint {
	if pod == nil || container == nil {
		klog.Errorf("[qosresourcemanager] GetTopologyHints got nil pod: %v or container: %v", pod, container)
		return nil
	}

	if isSkippedPod(pod, true, PodHasAnyTargetAnnoKey(pod, m.podSkipAnnotationKeys)) {
		klog.V(4).Infof("[qosresourcemanager] skip get topology hints for pod")
		return nil
	}

	containerType, containerIndex, err := GetContainerTypeAndIndex(pod, container)

	if err != nil {
		klog.Errorf("[qosresourcemanager] GetContainerTypeAndIndex failed with error: %v", err)
		return nil
	}

	// Garbage collect any stranded resource resources before providing TopologyHints
	m.UpdateAllocatedResources()

	// Loop through all resources and generate TopologyHints for them.
	resourceHints := make(map[string][]topologymanager.TopologyHint)
	for resourceObj, requestedObj := range container.Resources.Requests {
		reqResource := string(resourceObj)
		requested := int(requestedObj.Value())

		resource, err := m.getMappedResourceName(reqResource, container.Resources.Requests)

		if err != nil {
			klog.Errorf("[qosresourcemanager] getMappedResourceName failed with error: %v", err)
			return nil
		}

		klog.Infof("[qosresourcemanager] pod: %s/%s container: %s needs %d %s, to get topology hint",
			pod.Namespace, pod.Name, container.Name, requested, resource)

		// Only consider resources associated with a resource plugin.
		if m.isResourcePluginResource(resource) && !requestedObj.IsZero() {
			// Only consider resources that are actually with topology alignment
			if aligned := m.resourceHasTopologyAlignment(resource); !aligned {
				klog.Infof("[qosresourcemanager] resource '%v' does not have a topology preference", resource)
				resourceHints[resource] = nil
				continue
			}

			// Short circuit to regenerate the same hints if there are already
			// resources allocated to the Container. This might happen after a
			// kubelet restart, for example.
			allocationInfo := m.podResources.containerResource(string(pod.UID), container.Name, resource)
			if allocationInfo != nil && allocationInfo.ResourceHints != nil && len(allocationInfo.ResourceHints.Hints) > 0 {

				allocated := int(math.Ceil(allocationInfo.AllocatedQuantity))

				if allocationInfo.IsScalarResource && allocated >= requested {
					resourceHints[resource] = ParseListOfTopologyHints(allocationInfo.ResourceHints)
					klog.Warningf("[qosresourcemanager] resource %s already allocated to (pod %s/%s, container %v) with larger number than request: requested: %d, allocated: %d; not to getTopologyHints",
						resource, pod.GetNamespace(), pod.GetName(), container.Name, requested, allocated)
					continue
				} else {
					klog.Warningf("[qosresourcemanager] resource %s already allocated to (pod %s/%s, container %v) with smaller number than request: requested: %d, allocated: %d; continue to getTopologyHints",
						resource, pod.GetNamespace(), pod.GetName(), container.Name, requested, int(math.Ceil(allocationInfo.AllocatedQuantity)))
				}
			}

			startRPCTime := time.Now()
			m.mutex.Lock()
			eI, ok := m.endpoints[resource]
			m.mutex.Unlock()
			if !ok {
				klog.Errorf("[qosresourcemanager] unknown Resource Plugin %s", resource)
				resourceHints[resource] = []topologymanager.TopologyHint{}
				continue
			}

			klog.Infof("[qosresourcemanager] making GetTopologyHints request of %.3f resources %s for pod: %s/%s, container: %s",
				ParseQuantityToFloat64(requestedObj), resource, pod.Namespace, pod.Name, container.Name)

			resourceReq := &pluginapi.ResourceRequest{
				PodUid:         string(pod.GetUID()),
				PodNamespace:   pod.GetNamespace(),
				PodName:        pod.GetName(),
				ContainerName:  container.Name,
				ContainerType:  containerType,
				ContainerIndex: containerIndex,
				PodRole:        pod.Labels[pluginapi.PodRoleLabelKey],
				PodType:        pod.Annotations[pluginapi.PodTypeAnnotationKey],
				Labels:         maputil.CopySS(pod.Labels),
				Annotations:    maputil.CopySS(pod.Annotations),
				// use mapped resource name in "ResourceName" to indicates which endpoint to request
				ResourceName: resource,
				// use original requested resource name in "ResourceRequests" in order to make plugin identity real requested resource name
				ResourceRequests: map[string]float64{reqResource: ParseQuantityToFloat64(requestedObj)},
			}

			resp, err := eI.e.getTopologyHints(context.Background(), resourceReq)
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

			klog.Infof("[qosresourcemanager] GetTopologyHints for resource: %s, pod: %s/%s; container: %s, result: %+v",
				resource, pod.Namespace, pod.Name, container.Name, resourceHints[resource])
		}
	}

	return resourceHints
}

func (m *ManagerImpl) resourceHasTopologyAlignment(resource string) bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	eI, ok := m.endpoints[resource]
	if !ok {
		return false
	}

	if eI.opts == nil || !eI.opts.WithTopologyAlignment {
		klog.V(4).Infof("[qosresourcemanager] resource plugin options indicates that resource: %s without topology alignment", resource)
		return false
	}

	return true
}
