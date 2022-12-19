package qosresourcemanager

import (
	"context"
	"path"
	"testing"

	"github.com/stretchr/testify/require"

	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
)

var (
	eSocketName = "mock.sock"
)

func TestNewEndpoint(t *testing.T) {
	socket := path.Join("/tmp", eSocketName)

	p, e := eSetup(t, socket, "mock")
	defer eCleanup(t, p, e)
}

func TestAllocate(t *testing.T) {
	socket := path.Join("/tmp", eSocketName)
	p, e := eSetup(t, socket, "mock")
	defer eCleanup(t, p, e)

	req := generateResourceRequest()
	resp := generateResourceResponse()

	p.SetAllocFunc(func(r *pluginapi.ResourceRequest) (*pluginapi.ResourceAllocationResponse, error) {
		return resp, nil
	})

	respOut, err := e.allocate(context.TODO(), req)
	require.NoError(t, err)
	require.Equal(t, resp, respOut)
}

func generateResourceRequest() *pluginapi.ResourceRequest {
	return &pluginapi.ResourceRequest{
		PodUid:        "mock_pod",
		PodNamespace:  "mock_pod_ns",
		PodName:       "mock_pod_name",
		ContainerName: "mock_con_name",
		//IsInitContainer: false,
		PodRole:      "mock_role",
		PodType:      "mock_type",
		ResourceName: "mock_res",
		Hint: &pluginapi.TopologyHint{
			Nodes:     []uint64{0, 1},
			Preferred: true,
		},
		ResourceRequests: map[string]float64{
			"mock_res": 2,
		},
	}
}

func generateResourceResponse() *pluginapi.ResourceAllocationResponse {
	return &pluginapi.ResourceAllocationResponse{
		PodUid:        "mock_pod",
		PodNamespace:  "mock_pod_ns",
		PodName:       "mock_pod_name",
		ContainerName: "mock_con_name",
		//IsInitContainer: false,
		PodRole:      "mock_role",
		PodType:      "mock_type",
		ResourceName: "mock_res",
		AllocationResult: &pluginapi.ResourceAllocation{
			ResourceAllocation: map[string]*pluginapi.ResourceAllocationInfo{
				"mock_res": generateResourceAllocationInfo(),
			},
		},
	}
}

// todo, only containers setAllocFunc, wo we can't mock the testing cases for other RPC calls
//  actually, since endpoint for qrm only performs as a pure proxy, there is no need to add
//  more test cases before its interface changes to list-watch
func generateGetTopologyAwareAllocatableResourcesRequest() *pluginapi.GetTopologyAwareAllocatableResourcesRequest {
	return &pluginapi.GetTopologyAwareAllocatableResourcesRequest{}
}

func generateGetTopologyAwareResourcesRequest() *pluginapi.GetTopologyAwareResourcesRequest {
	return &pluginapi.GetTopologyAwareResourcesRequest{
		PodUid:        "mock_pod",
		ContainerName: "mock_con_name",
	}
}

func generateGetResourcesAllocationRequest() *pluginapi.GetResourcesAllocationRequest {
	return &pluginapi.GetResourcesAllocationRequest{}
}

func eSetup(t *testing.T, socket, resourceName string) (*Stub, *endpointImpl) {
	p := NewResourcePluginStub(socket, resourceName, false)

	err := p.Start()
	require.NoError(t, err)

	e, err := newEndpointImpl(socket, resourceName)
	require.NoError(t, err)

	return p, e
}

func eCleanup(t *testing.T, p *Stub, e *endpointImpl) {
	p.Stop()
	e.stop()
}
