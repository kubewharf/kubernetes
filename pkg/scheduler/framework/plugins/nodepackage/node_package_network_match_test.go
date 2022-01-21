package nodepackage

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"code.byted.org/kubernetes/apis/k8s/non.native.resource/v1alpha1"
	nonnativeresourcelisters "code.byted.org/kubernetes/clientsets/k8s/listers/non.native.resource/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/features"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
	"k8s.io/kubernetes/pkg/scheduler/internal/cache"
	"k8s.io/kubernetes/pkg/scheduler/util"
)

func TestMatchNodePackageNBW(t *testing.T) {
	tests := []struct {
		name             string
		pod              *v1.Pod
		nodes            []*v1.Node
		refinedResources []*v1alpha1.RefinedNodeResource
		expectedList     framework.NodeScoreList
	}{
		{
			name:             "0 Gbps requested, differently sized machines",
			pod:              makePod(0),
			nodes:            []*v1.Node{makeNode("node1"), makeNode("node2"), makeNode("node3"), makeNode("node4")},
			refinedResources: []*v1alpha1.RefinedNodeResource{makeRefinedNodeResource(0, "node1"), makeRefinedNodeResource(10000, "node2"), makeRefinedNodeResource(25000, "node3"), makeRefinedNodeResource(100000, "node4")},
			expectedList:     []framework.NodeScore{{Name: "node1", Score: 100}, {"node2", 90}, {"node3", 75}, {"node4", 0}},
		},
		{
			name:             "10 Gbps requested, differently sized machines",
			pod:              makePod(10000),
			nodes:            []*v1.Node{makeNode("node1"), makeNode("node2"), makeNode("node3")},
			refinedResources: []*v1alpha1.RefinedNodeResource{makeRefinedNodeResource(10000, "node1"), makeRefinedNodeResource(25000, "node2"), makeRefinedNodeResource(100000, "node3")},
			expectedList:     []framework.NodeScore{{Name: "node1", Score: 100}, {"node2", 83}, {"node3", 0}},
		},
		{
			name:             "25 Gbps requested, differently sized machines",
			pod:              makePod(25000),
			nodes:            []*v1.Node{makeNode("node1"), makeNode("node2")},
			refinedResources: []*v1alpha1.RefinedNodeResource{makeRefinedNodeResource(25000, "node1"), makeRefinedNodeResource(100000, "node2")},
			expectedList:     []framework.NodeScore{{Name: "node1", Score: 100}, {"node2", 0}},
		},
		{
			name:             "100 Gbps requested, differently sized machines",
			pod:              makePod(100000),
			nodes:            []*v1.Node{makeNode("node1")},
			refinedResources: []*v1alpha1.RefinedNodeResource{makeRefinedNodeResource(100000, "node1")},
			expectedList:     []framework.NodeScore{{Name: "node1", Score: 0}},
		},
	}
	utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=true", features.NonNativeResourceSchedulingSupport))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := cache.NewSnapshot(nil, test.nodes)
			fh, _ := framework.NewFramework(nil, nil, nil, framework.WithSnapshotSharedLister(snapshot))

			nbwAdaptation := MatchNodePackageNBW{
				handle:                    fh,
				refinedNodeResourceLister: newFakeRefinedNodeResourceLister(test.refinedResources),
			}

			var gotList framework.NodeScoreList
			for _, node := range test.nodes {
				got, err := nbwAdaptation.Score(context.Background(), nil, test.pod, node.Name)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				gotList = append(gotList, framework.NodeScore{Name: node.Name, Score: got})
			}
			status := nbwAdaptation.ScoreExtensions().NormalizeScore(context.Background(), nil, test.pod, gotList)
			if status != nil {
				t.Errorf("unexpected error: %v", status)
			}
			for i, gotScore := range gotList {
				expectScore := test.expectedList[i]
				if gotScore != expectScore {
					t.Errorf("expect score list: %v, but got: %v", test.expectedList, gotList)
				}
			}
		})
	}
}

func makePod(nbwRequest int64) *v1.Pod {
	annotations := map[string]string{
		util.NumericResourcesRequests: util.NBWRefinedResourceKey + ">=" + strconv.Itoa(int(nbwRequest)),
	}
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: annotations},
	}
}

func makeNode(nodeName string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
	}
}

func makeRefinedNodeResource(nbwCapacity int64, nodeName string) *v1alpha1.RefinedNodeResource {
	pattern := v1alpha1.NumericResourcePropertyPattern{
		PropertyName:             util.NBWRefinedResourceKey,
		PropertyAllocatableValue: *resource.NewQuantity(nbwCapacity, resource.DecimalSI),
		PropertyCapacityValue:    *resource.NewQuantity(nbwCapacity, resource.DecimalSI),
	}
	return &v1alpha1.RefinedNodeResource{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Status: v1alpha1.RefinedNodeResourceStatus{
			NumericResource: v1alpha1.NumericResourceProperties{
				NumericProperties: []v1alpha1.NumericResourcePropertyPattern{pattern}},
		},
	}
}

// test helper methods below

type fakeRefinedNodeResourceLister struct {
	refinedNodeResources map[string]*v1alpha1.RefinedNodeResource
}

func newFakeRefinedNodeResourceLister(refinedNodeResources []*v1alpha1.RefinedNodeResource) nonnativeresourcelisters.RefinedNodeResourceLister {
	refinedNodeResourcesMap := make(map[string]*v1alpha1.RefinedNodeResource)
	for _, refinedNodeResource := range refinedNodeResources {
		refinedNodeResourcesMap[refinedNodeResource.Name] = refinedNodeResource
	}
	return &fakeRefinedNodeResourceLister{
		refinedNodeResources: refinedNodeResourcesMap,
	}
}

// not used
func (f fakeRefinedNodeResourceLister) List(selector labels.Selector) (ret []*v1alpha1.RefinedNodeResource, err error) {
	for _, pg := range f.refinedNodeResources {
		ret = append(ret, pg)
	}
	return ret, err
}

func (f fakeRefinedNodeResourceLister) Get(name string) (*v1alpha1.RefinedNodeResource, error) {
	if pg, ok := f.refinedNodeResources[name]; ok {
		return pg, nil
	} else {
		return nil, apierrors.NewNotFound(v1alpha1.Resource("podgroup"), name)
	}
}
