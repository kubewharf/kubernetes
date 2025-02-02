/*
Copyright 2020 The Kubernetes Authors.

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

package topologymanager

import (
	"k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/cm/admission"
	"k8s.io/kubernetes/pkg/kubelet/cm/containermap"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

type podScope struct {
	scope
}

// Ensure podScope implements Scope interface
var _ Scope = &podScope{}

// NewPodScope returns a pod scope.
func NewPodScope(policy Policy) Scope {
	return &podScope{
		scope{
			name:             podTopologyScope,
			podTopologyHints: map[string]podTopologyHints{},
			policy:           policy,
			podMap:           containermap.NewContainerMap(),
		},
	}
}

func (s *podScope) Admit(pod *v1.Pod) lifecycle.PodAdmitResult {
	// Exception - Policy : none
	if s.policy.Name() == PolicyNone {
		return s.admitPolicyNoneForPod(pod)
	}

	bestHint, admit := s.calculateAffinity(pod)
	klog.InfoS("Best TopologyHint", "bestHint", bestHint, "pod", klog.KObj(pod))
	if !admit {
		return admission.GetPodAdmitResult(&TopologyAffinityError{})
	}

	klog.Infof("[topologymanager] Topology Affinity for (pod: %v): %v", klog.KObj(pod), bestHint)
	s.setTopologyHints(string(pod.UID), string(pod.UID), bestHint)
	if err := s.allocateAlignedResourcesForPod(pod); err != nil {
		return unexpectedAdmissionError(err)
	}
	return admission.GetPodAdmitResult(nil)
}

func (s *podScope) accumulateProvidersHints(pod *v1.Pod) []map[string][]TopologyHint {
	var providersHints []map[string][]TopologyHint

	for _, provider := range s.hintProviders {
		// Get the TopologyHints for a Pod from a provider.
		hints := provider.GetPodTopologyHints(pod)
		providersHints = append(providersHints, hints)
		klog.InfoS("TopologyHints", "hints", hints, "pod", klog.KObj(pod))
	}
	return providersHints
}

func (s *podScope) calculateAffinity(pod *v1.Pod) (map[string]TopologyHint, bool) {
	providersHints := s.accumulateProvidersHints(pod)
	bestHint, admit := s.policy.Merge(providersHints)
	klog.InfoS("PodTopologyHint", "bestHint", bestHint)
	return bestHint, admit
}
