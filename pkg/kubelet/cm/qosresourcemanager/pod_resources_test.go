package qosresourcemanager

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/sets"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
)

func TestPodResources(t *testing.T) {
	podResources := newPodResourcesChk()

	type resAllocation struct {
		resName    string
		allocation *pluginapi.ResourceAllocationInfo
	}

	normalAllocation := generateResourceAllocationInfo()

	normalAllocation2 := generateResourceAllocationInfo()
	normalAllocation2.Annotations["mock_key_2"] = "mock_ano_2"
	normalAllocation2.OciPropertyName = "CpusetMems"
	normalAllocation2.AllocationResult = "1,2"

	overrideAllocation := generateResourceAllocationInfo()
	overrideAllocation.Envs["mock_key"] = "mock_env_2"
	overrideAllocation.Annotations["mock_key_2"] = "mock_ano_2"
	overrideAllocation.AllocatedQuantity = 4

	invalidAllocation := generateResourceAllocationInfo()
	invalidAllocation.OciPropertyName = "mock"

	type testCase struct {
		// inserted pod resources
		description string
		podUID      string
		conName     string
		allocations []resAllocation

		// testing results
		resConResource    ContainerResources
		resScalarResource map[string]float64
		resResourceNames  sets.String
		resOptions        *kubecontainer.ResourceRunContainerOptions
		resOptionsErr     error
	}
	testCases := []testCase{
		{
			description: "insert pod resources with whole info",
			podUID:      "mock_pod",
			conName:     "mock_con",
			allocations: []resAllocation{
				{resName: "mock_res", allocation: normalAllocation},
			},

			resConResource: ContainerResources{
				"mock_con": {
					"mock_res": normalAllocation,
				},
			},
			resScalarResource: map[string]float64{"mock_res": 3},
			resResourceNames:  sets.NewString("mock_res"),
			resOptions: &kubecontainer.ResourceRunContainerOptions{
				Envs: []kubecontainer.EnvVar{
					{
						Name:  "mock_key",
						Value: "mock_env",
					},
				},
				Annotations: []kubecontainer.Annotation{
					{
						Name:  "mock_key",
						Value: "mock_ano",
					},
				},
				Resources: &runtimeapi.LinuxContainerResources{
					CpusetCpus: "5-6,10",
				},
			},
			resOptionsErr: nil,
		},
		{
			description: "insert pod resources with multiple resources",
			podUID:      "mock_pod",
			conName:     "mock_con",
			allocations: []resAllocation{
				{resName: "mock_res", allocation: normalAllocation},
				{resName: "mock_res_2", allocation: normalAllocation2},
			},

			resConResource: ContainerResources{
				"mock_con": {
					"mock_res":   normalAllocation,
					"mock_res_2": normalAllocation2,
				},
			},
			resScalarResource: map[string]float64{"mock_res": 3, "mock_res_2": 3},
			resResourceNames:  sets.NewString("mock_res", "mock_res_2"),
			resOptions: &kubecontainer.ResourceRunContainerOptions{
				Envs: []kubecontainer.EnvVar{
					{
						Name:  "mock_key",
						Value: "mock_env",
					},
				},
				Annotations: []kubecontainer.Annotation{
					{
						Name:  "mock_key",
						Value: "mock_ano",
					},
					{
						Name:  "mock_key_2",
						Value: "mock_ano_2",
					},
				},
				Resources: &runtimeapi.LinuxContainerResources{
					CpusetCpus: "5-6,10",
					CpusetMems: "1,2",
				},
			},
			resOptionsErr: nil,
		},
		{
			description: "override pod resources with whole info",
			podUID:      "mock_pod",
			conName:     "mock_con",
			allocations: []resAllocation{
				{resName: "mock_res", allocation: normalAllocation},
				{resName: "mock_res", allocation: overrideAllocation},
			},

			resConResource: ContainerResources{
				"mock_con": {
					"mock_res": overrideAllocation,
				},
			},
			resScalarResource: map[string]float64{"mock_res": 4},
			resResourceNames:  sets.NewString("mock_res"),
			resOptions: &kubecontainer.ResourceRunContainerOptions{
				Envs: []kubecontainer.EnvVar{
					{
						Name:  "mock_key",
						Value: "mock_env_2",
					},
				},
				Annotations: []kubecontainer.Annotation{
					{
						Name:  "mock_key",
						Value: "mock_ano",
					},
					{
						Name:  "mock_key_2",
						Value: "mock_ano_2",
					},
				},
				Resources: &runtimeapi.LinuxContainerResources{
					CpusetCpus: "5-6,10",
				},
			},
			resOptionsErr: nil,
		},
		{
			description: "insert pod resources with invalid oci config",
			podUID:      "mock_pod",
			conName:     "mock_con",
			allocations: []resAllocation{
				{resName: "mock_res", allocation: invalidAllocation},
			},

			resConResource: ContainerResources{
				"mock_con": {
					"mock_res": invalidAllocation,
				},
			},
			resScalarResource: map[string]float64{"mock_res": 3},
			resResourceNames:  sets.NewString("mock_res"),
			resOptionsErr:     errors.New(""),
		},
	}

	convertENVToMap := func(envs []kubecontainer.EnvVar) map[string]string {
		res := make(map[string]string)
		for _, env := range envs {
			res[env.Name] = env.Value
		}
		return res
	}
	convertAnnotationToMap := func(annotations []kubecontainer.Annotation) map[string]string {
		res := make(map[string]string)
		for _, ano := range annotations {
			res[ano.Name] = ano.Value
		}
		return res
	}
	check := func(prefix string, tc testCase) {
		resConResource := podResources.podResources(tc.podUID)
		resScalarResource := podResources.scalarResourcesQuantity()
		resResourceNames := podResources.allAllocatedResourceNames()
		resOptions, resOptionsErr := podResources.resourceRunContainerOptions(tc.podUID, tc.conName)

		require.Equal(t, resConResource, tc.resConResource, "%v/%v: pod container resources not equal", prefix, tc.description)
		require.Equal(t, resScalarResource, tc.resScalarResource, "%v/%v: scalar resources not equal", prefix, tc.description)
		require.Equal(t, resResourceNames, tc.resResourceNames, "%v/%v: all resource names not equal", prefix, tc.description)
		require.Equal(t, resOptions == nil, tc.resOptions == nil, "%v/%v: container options not equal", prefix, tc.description)
		require.Equal(t, resOptionsErr == nil, tc.resOptionsErr == nil, "%v/%v: container options error not equal", prefix, tc.description)
		if resOptions != nil && tc.resOptions != nil {
			require.Equal(t, resOptions.Resources, tc.resOptions.Resources, "%v/%v: container options [resources] not equal", prefix, tc.description)
			require.Equal(t, convertENVToMap(resOptions.Envs), convertENVToMap(tc.resOptions.Envs), "%v/%v: container options [envs] not equal", prefix, tc.description)
			require.Equal(t, convertAnnotationToMap(resOptions.Annotations), convertAnnotationToMap(tc.resOptions.Annotations), "%v/%v: container options [annotations] not equal", prefix, tc.description)
		}
	}

	for _, tc := range testCases {
		t.Logf("%v", tc.description)
		for _, a := range tc.allocations {
			podResources.insert(tc.podUID, tc.conName, a.resName, a.allocation)
		}

		check("before reloading checkpoint", tc)
		data := podResources.toCheckpointData()

		podResources.delete([]string{tc.podUID})
		podResources.fromCheckpointData(data)
		check("after reloading checkpoint", tc)

		podResources.delete([]string{tc.podUID})
	}
}

func generateResourceAllocationInfo() *pluginapi.ResourceAllocationInfo {
	return &pluginapi.ResourceAllocationInfo{
		OciPropertyName:    "CpusetCpus",
		IsNodeResource:     true,
		IsScalarResource:   true,
		AllocatedQuantity:  3,
		AllocationResult: "5-6,10",
		Envs:               map[string]string{"mock_key": "mock_env"},
		Annotations:        map[string]string{"mock_key": "mock_ano"},
		ResourceHints:      &pluginapi.ListOfTopologyHints{},
	}
}
