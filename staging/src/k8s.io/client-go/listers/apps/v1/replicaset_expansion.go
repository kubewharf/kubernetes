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

package v1

import (
	"fmt"

	apps "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

// ReplicaSetListerExpansion allows custom methods to be added to
// ReplicaSetLister.
type ReplicaSetListerExpansion interface {
	GetPodReplicaSets(pod *v1.Pod) ([]*apps.ReplicaSet, error)
	ReplicaSetsForTCELabel(namespace, indexName, indexKey string) ReplicaSetTCELabelLister
}

func (s *replicaSetLister) ReplicaSetsForTCELabel(namespace, indexName, indexKey string) ReplicaSetTCELabelLister {
	return replicasetTCELabelLister{
		indexer:   s.indexer,
		namespace: namespace,
		indexName: indexName,
		indexKey:  indexKey,
	}
}

type ReplicaSetTCELabelLister interface {
	List(labelSelector *metav1.LabelSelector) (ret []*apps.ReplicaSet, err error)
}

type replicasetTCELabelLister struct {
	indexer   cache.Indexer
	namespace string
	indexName string
	indexKey  string
}

func (s replicasetTCELabelLister) List(labelSelector *metav1.LabelSelector) (ret []*apps.ReplicaSet, err error) {
	if _, exist := labelSelector.MatchLabels[s.indexKey]; !exist {
		// If index key is not exist, fallback to slow path
		selector, err := metav1.LabelSelectorAsSelector(labelSelector)
		if err != nil {
			return ret, err
		}
		err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
			ret = append(ret, m.(*apps.ReplicaSet))
		})
		return ret, err
	}

	items, err := s.indexer.Index(s.indexName, &metav1.ObjectMeta{Labels: labelSelector.MatchLabels})
	if err != nil {
		// Ignore error; do slow search without index.
		klog.Warningf("can not retrieve list of objects using index : %v", err)

		selector, err := metav1.LabelSelectorAsSelector(labelSelector)
		if err != nil {
			return ret, err
		}
		for _, m := range s.indexer.List() {
			metadata, err := meta.Accessor(m)
			if err != nil {
				return nil, err
			}
			if metadata.GetNamespace() == s.namespace && selector.Matches(labels.Set(metadata.GetLabels())) {
				ret = append(ret, m.(*apps.ReplicaSet))
			}
		}
		return ret, nil
	}
	for _, m := range items {
		ret = append(ret, m.(*apps.ReplicaSet))
	}
	return ret, nil
}

// ReplicaSetNamespaceListerExpansion allows custom methods to be added to
// ReplicaSetNamespaceLister.
type ReplicaSetNamespaceListerExpansion interface{}

// GetPodReplicaSets returns a list of ReplicaSets that potentially match a pod.
// Only the one specified in the Pod's ControllerRef will actually manage it.
// Returns an error only if no matching ReplicaSets are found.
func (s *replicaSetLister) GetPodReplicaSets(pod *v1.Pod) ([]*apps.ReplicaSet, error) {
	if len(pod.Labels) == 0 {
		return nil, fmt.Errorf("no ReplicaSets found for pod %v because it has no labels", pod.Name)
	}

	list, err := s.ReplicaSets(pod.Namespace).List(labels.Everything())
	if err != nil {
		return nil, err
	}

	var rss []*apps.ReplicaSet
	for _, rs := range list {
		if rs.Namespace != pod.Namespace {
			continue
		}
		selector, err := metav1.LabelSelectorAsSelector(rs.Spec.Selector)
		if err != nil {
			return nil, fmt.Errorf("invalid selector: %v", err)
		}

		// If a ReplicaSet with a nil or empty selector creeps in, it should match nothing, not everything.
		if selector.Empty() || !selector.Matches(labels.Set(pod.Labels)) {
			continue
		}
		rss = append(rss, rs)
	}

	if len(rss) == 0 {
		return nil, fmt.Errorf("could not find ReplicaSet for pod %s in namespace %s with labels: %v", pod.Name, pod.Namespace, pod.Labels)
	}

	return rss, nil
}
