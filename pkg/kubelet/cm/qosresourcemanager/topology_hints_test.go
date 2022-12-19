package qosresourcemanager

import (
	"context"
	"io/ioutil"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager/bitmask"
)

type mockAffinityStore struct {
	hint topologymanager.TopologyHint
}

func (m *mockAffinityStore) GetAffinity(podUID string, containerName string) topologymanager.TopologyHint {
	return m.hint
}

func makeSocketMask(sockets ...int) bitmask.BitMask {
	mask, _ := bitmask.NewBitMask(sockets...)
	return mask
}

// since topology allocation logic isn't performed in manager (decided only by qrm plugin)
// so topology hints testing is more like endpoint
// todo. since we don't have setGetTopologyHints for stub, so it's invalid to test allocation logic for now.
func TestGetTopologyHints(t *testing.T) {
	p1, e1 := eSetup(t, "/tmp/mock_1", "mock_res")
	defer eCleanup(t, p1, e1)

	e2 := endpointInfo{e: &MockEndpoint{topologyHints: func(ctx context.Context, resourceRequest *pluginapi.ResourceRequest) (*pluginapi.ResourceHintsResponse, error) {
		resp := &pluginapi.ResourceHintsResponse{}
		resp.ResourceHints = make(map[string]*pluginapi.ListOfTopologyHints)
		resp.ResourceHints["mock_res_1"] = &pluginapi.ListOfTopologyHints{
			Hints: []*pluginapi.TopologyHint{
				{Nodes: []uint64{0}, Preferred: true},
				{Nodes: []uint64{1}, Preferred: true},
				{Nodes: []uint64{0, 1}, Preferred: false},
			},
		}

		return resp, nil
	}}, opts: &pluginapi.ResourcePluginOptions{
		WithTopologyAlignment: true,
	}}

	hint0, _ := bitmask.NewBitMask(0)
	hint1, _ := bitmask.NewBitMask(1)
	hint2, _ := bitmask.NewBitMask(0, 1)

	testCases := []struct {
		description   string
		pod           *v1.Pod
		endpoints     map[string]endpointInfo
		expectedHints map[string][]topologymanager.TopologyHint
	}{
		{
			description: "skipped pod should not have hints",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{Kind: DaemonsetKind},
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Resources: v1.ResourceRequirements{
								Limits: v1.ResourceList{
									v1.ResourceName("mock_res_1"): resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
		},
		{
			description: "resources without plugin should not have hints",
			pod: &v1.Pod{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Resources: v1.ResourceRequirements{
								Limits: v1.ResourceList{
									v1.ResourceName("mock_res_1"): resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
		},
		{
			description: "resources with zero request should not have hints",
			pod: &v1.Pod{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Resources: v1.ResourceRequirements{
								Limits: v1.ResourceList{
									v1.ResourceName("mock_res_1"): resource.MustParse("0"),
								},
							},
						},
					},
				},
			},
			endpoints: map[string]endpointInfo{"mock_res_1": {e: e1}},
		},
		{
			description: "resources with request and endpoint should have hints",
			pod: &v1.Pod{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{
									v1.ResourceName("mock_res_1"): resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
			endpoints: map[string]endpointInfo{"mock_res_1": e2},
			expectedHints: map[string][]topologymanager.TopologyHint{
				"mock_res_1": {
					{NUMANodeAffinity: hint0, Preferred: true},
					{NUMANodeAffinity: hint1, Preferred: true},
					{NUMANodeAffinity: hint2, Preferred: false},
				},
			},
		},
	}

	for _, tc := range testCases {
		m := ManagerImpl{
			podResources: newPodResourcesChk(),
			sourcesReady: &sourcesReadyStub{},
			activePods:   func() []*v1.Pod { return []*v1.Pod{tc.pod} },
			endpoints:    tc.endpoints,
		}

		hints := m.GetTopologyHints(tc.pod, &tc.pod.Spec.Containers[0])
		for r := range tc.expectedHints {
			sort.SliceStable(hints[r], func(i, j int) bool {
				return hints[r][i].LessThan(hints[r][j])
			})
			sort.SliceStable(tc.expectedHints[r], func(i, j int) bool {
				return tc.expectedHints[r][i].LessThan(tc.expectedHints[r][j])
			})
			if !reflect.DeepEqual(hints[r], tc.expectedHints[r]) {
				t.Errorf("%v: Expected result to be %v, got %v", tc.description, tc.expectedHints[r], hints[r])
			}
		}
	}
}

func TestResourceHasTopologyAlignment(t *testing.T) {
	res1 := TestResource{
		resourceName:     "domain1.com/resource1",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}
	res2 := TestResource{
		resourceName:     "domain2.com/resource2",
		resourceQuantity: *resource.NewQuantity(int64(1), resource.DecimalSI),
	}
	testResources := make([]TestResource, 2)
	testResources = append(testResources, res1)
	testResources = append(testResources, res2)
	as := require.New(t)

	testPods := []*v1.Pod{
		makePod("Pod0", v1.ResourceList{
			v1.ResourceName(res1.resourceName): res1.resourceQuantity}),
		makePod("Pod1", v1.ResourceList{
			v1.ResourceName(res2.resourceName): res2.resourceQuantity}),
	}
	testPods[0].Spec.Containers[0].Name = "Cont0"
	testPods[1].Spec.Containers[0].Name = "Cont1"
	podsStub := activePodsStub{
		activePods: []*v1.Pod{},
	}
	tmpDir, err := ioutil.TempDir("", "checkpoint")
	as.Nil(err)
	defer os.RemoveAll(tmpDir)
	testManager, err := getTestManager(tmpDir, podsStub.getActivePods, testResources)
	as.Nil(err)

	//Doesn't have
	testManager.endpoints[res1.resourceName].opts.WithTopologyAlignment = false
	Alignment := testManager.resourceHasTopologyAlignment(res1.resourceName)
	as.Equal(false, Alignment)
	//Has
	testManager.endpoints[res2.resourceName].opts.WithTopologyAlignment = true
	Alignment = testManager.resourceHasTopologyAlignment(res2.resourceName)
	as.Equal(true, Alignment)
}
