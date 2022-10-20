package qosresourcemanager

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/tools/record"
	apitest "k8s.io/cri-api/pkg/apis/testing"
	"k8s.io/klog/v2"
	watcherapi "k8s.io/kubelet/pkg/apis/pluginregistration/v1"
	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
	apipod "k8s.io/kubernetes/pkg/api/pod"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/config"
	"k8s.io/kubernetes/pkg/kubelet/container"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/pluginmanager"
	"k8s.io/kubernetes/pkg/kubelet/status"
	statustest "k8s.io/kubernetes/pkg/kubelet/status/testing"
	schedulerframework "k8s.io/kubernetes/pkg/scheduler/framework"
)

const (
	testResourceName        = "mock_res"
	PreStartAnnotationKey   = "prestartCalled"
	PreStartAnnotationValue = "true"
)

func tmpSocketDir() (socketDir, socketName, pluginSocketName string, err error) {
	socketDir, err = ioutil.TempDir("", "qrm")
	if err != nil {
		return
	}
	socketName = path.Join(socketDir, "server.sock")
	pluginSocketName = path.Join(socketDir, "qrm-plugin.sock")
	_ = os.MkdirAll(socketDir, 0755)
	return
}

func TestNewManagerImpl(t *testing.T) {
	socketDir, socketName, _, err := tmpSocketDir()
	topologyStore := topologymanager.NewFakeManager()
	require.NoError(t, err)
	defer os.RemoveAll(socketDir)

	_, err = newManagerImpl(socketName, nil, topologyStore, time.Second)
	require.NoError(t, err)
}

func TestNewManagerImplStart(t *testing.T) {
	socketDir, socketName, pluginSocketName, err := tmpSocketDir()
	require.NoError(t, err)
	defer os.RemoveAll(socketDir)

	m, p := setup(t, socketName, pluginSocketName)

	// ensures register successful after start server and plugin
	err = p.Register(socketName, testResourceName, socketDir)
	require.Nil(t, err)
	_, exists := m.endpoints[testResourceName]
	require.True(t, exists)

	cleanup(t, m, p, nil)
	// Stop should tolerate being called more than once
	cleanup(t, m, p, nil)
}

func TestNewManagerImplStartProbeMode(t *testing.T) {
	socketDir, socketName, pluginSocketName, err := tmpSocketDir()
	require.NoError(t, err)
	defer os.RemoveAll(socketDir)

	m, p, _, stopCh := setupInProbeMode(t, socketName, pluginSocketName)
	// make plugin register to QRM automatically by plugin manager
	time.Sleep(time.Second)
	_, exists := m.endpoints[testResourceName]
	require.True(t, exists)
	cleanup(t, m, p, stopCh)
}

type MockEndpoint struct {
	allocateFunc        func(resourceRequest *pluginapi.ResourceRequest) (*pluginapi.ResourceAllocationResponse, error)
	topologyAllocatable func(ctx context.Context, request *pluginapi.GetTopologyAwareAllocatableResourcesRequest) (*pluginapi.GetTopologyAwareAllocatableResourcesResponse, error)
	topologyAllocated   func(ctx context.Context, request *pluginapi.GetTopologyAwareResourcesRequest) (*pluginapi.GetTopologyAwareResourcesResponse, error)
	topologyHints       func(ctx context.Context, resourceRequest *pluginapi.ResourceRequest) (*pluginapi.ResourceHintsResponse, error)
	resourceAlloc       func(ctx context.Context, request *pluginapi.GetResourcesAllocationRequest) (*pluginapi.GetResourcesAllocationResponse, error)
	initChan            chan []string
	stopTime            time.Time
}

func (m *MockEndpoint) stop() {
	m.stopTime = time.Now()
}
func (m *MockEndpoint) run(success chan<- bool) {}

func (m *MockEndpoint) preStartContainer(pod *v1.Pod, container *v1.Container) (*pluginapi.PreStartContainerResponse, error) {
	//m.initChan <- devs
	if pod == nil || container == nil {
		return nil, fmt.Errorf("preStartContainer met nil pod or container")
	}

	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}

	pod.Annotations[PreStartAnnotationKey] = PreStartAnnotationValue

	return &pluginapi.PreStartContainerResponse{}, nil
}

func (m *MockEndpoint) allocate(ctx context.Context, resourceRequest *pluginapi.ResourceRequest) (*pluginapi.ResourceAllocationResponse, error) {
	if m.isStopped() {
		return nil, fmt.Errorf(errEndpointStopped, m)
	}
	if m.allocateFunc != nil {
		return m.allocateFunc(resourceRequest)
	}
	return nil, nil
}

func (m *MockEndpoint) isStopped() bool {
	return !m.stopTime.IsZero()
}

var SGP int = 0

func (m *MockEndpoint) stopGracePeriodExpired() bool {
	if SGP == 0 {
		return false
	} else {
		return true
	}
}

func (m *MockEndpoint) getTopologyHints(ctx context.Context, request *pluginapi.ResourceRequest) (*pluginapi.ResourceHintsResponse, error) {
	if m.isStopped() {
		return nil, fmt.Errorf("plugin stopped")
	}
	if m.topologyHints != nil {
		return m.topologyHints(ctx, request)
	}
	return nil, nil
}

func (m *MockEndpoint) getTopologyAwareAllocatableResources(ctx context.Context, request *pluginapi.GetTopologyAwareAllocatableResourcesRequest) (*pluginapi.GetTopologyAwareAllocatableResourcesResponse, error) {
	if m.isStopped() {
		return nil, fmt.Errorf("plugin stopped")
	}
	if m.topologyAllocatable != nil {
		return m.topologyAllocatable(ctx, request)
	}
	return nil, nil
}

func (m *MockEndpoint) getTopologyAwareResources(ctx context.Context, request *pluginapi.GetTopologyAwareResourcesRequest) (*pluginapi.GetTopologyAwareResourcesResponse, error) {
	if m.isStopped() {
		return nil, fmt.Errorf("plugin stopped")
	}
	if m.topologyAllocated != nil {
		return m.topologyAllocated(ctx, request)
	}
	return nil, nil
}

func (m *MockEndpoint) removePod(ctx context.Context, removePodRequest *pluginapi.RemovePodRequest) (*pluginapi.RemovePodResponse, error) {
	return nil, nil
}

func (m *MockEndpoint) getResourceAllocation(ctx context.Context, request *pluginapi.GetResourcesAllocationRequest) (*pluginapi.GetResourcesAllocationResponse, error) {
	if m.resourceAlloc != nil {
		return m.resourceAlloc(ctx, request)
	}
	return nil, nil
}

func makePod(name string, rl v1.ResourceList) *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:  uuid.NewUUID(),
			Name: name,
			Labels: map[string]string{
				PSMLabelKey: name,
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name: name,
					Resources: v1.ResourceRequirements{
						Requests: rl.DeepCopy(),
						Limits:   rl.DeepCopy(),
					},
				},
			},
		},
	}
}

type TestResource struct {
	resourceName     string
	resourceQuantity resource.Quantity
}

type activePodsStub struct {
	activePods []*v1.Pod
}

func (a *activePodsStub) getActivePods() []*v1.Pod {
	return a.activePods
}

func (a *activePodsStub) updateActivePods(newPods []*v1.Pod) {
	a.activePods = newPods
}

func TestManagerAllocate(t *testing.T) {
	res1 := TestResource{
		resourceName:     "domain1.com/resource1",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}
	res2 := TestResource{
		resourceName:     "domain2.com/resource2",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}
	res3 := TestResource{
		resourceName:     "domain3.com/resource3",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}

	testResources := []TestResource{
		res1,
		res2,
		res3,
	}

	as := require.New(t)

	testPods := []*v1.Pod{
		makePod("Pod0", v1.ResourceList{
			v1.ResourceName(res1.resourceName): res1.resourceQuantity,
			v1.ResourceName(res2.resourceName): res2.resourceQuantity}),
		makePod("Pod1", v1.ResourceList{
			v1.ResourceName(res1.resourceName): res1.resourceQuantity}),
		makePod("Pod2", v1.ResourceList{
			v1.ResourceName(res2.resourceName): res2.resourceQuantity}),
		makePod("Pod3", v1.ResourceList{
			v1.ResourceName(res3.resourceName): res3.resourceQuantity}),
		makePod("Pod4", v1.ResourceList{
			v1.ResourceName(res3.resourceName): res3.resourceQuantity}),
	}

	podsStub := activePodsStub{
		activePods: testPods,
	}
	tmpDir, err := ioutil.TempDir("", "checkpoint")
	as.Nil(err)
	defer os.RemoveAll(tmpDir)

	testManager, err := getTestManager(tmpDir, podsStub.getActivePods, testResources)
	as.Nil(err)

	// test skipped pod
	setPodAnnotation(testPods[1], apipod.TCEDaemonPodAnnotationKey, "IsDaemonPod")
	err = testManager.Allocate(testPods[1], &testPods[1].Spec.Containers[0])
	// runContainerOpts1 and err are both nil
	runContainerOpts1, err := testManager.GetResourceRunContainerOptions(testPods[1], &testPods[1].Spec.Containers[0])
	as.Nil(err)
	as.Nil(runContainerOpts1)
	// resourceAllocation1 is nil
	resourceAllocation1 := testManager.podResources.containerResource(string(testPods[1].UID), testPods[1].Spec.Containers[0].Name, res1.resourceName)
	as.Nil(resourceAllocation1)

	// remove daemon annotation
	delete(testPods[1].Annotations, apipod.TCEDaemonPodAnnotationKey)

	// endpoint isStopped
	e1 := testManager.endpoints[res1.resourceName].e
	e1.stop()
	setPodAnnotation(testPods[1], pluginapi.PodTypeAnnotationKey, pluginapi.PodTypeBestEffort)
	err = testManager.Allocate(testPods[1], &testPods[1].Spec.Containers[0])
	as.NotNil(err)
	klog.Errorf("stopped endpoint allocation error: %v", err)

	// resume stopped endpoint
	registerEndpointByRes(testManager, testResources)

	// container resource allocated >= need
	err = testManager.Allocate(testPods[0], &testPods[0].Spec.Containers[0])
	as.Nil(err)
	// res1 and res2 in resourceAllocation0 are both not nil and with correct values
	resourceAllocation0 := testManager.podResources.containerAllResources(string(testPods[0].UID), testPods[0].Spec.Containers[0].Name)
	as.NotNil(resourceAllocation0)
	as.NotNil(resourceAllocation0[res1.resourceName])
	as.NotNil(resourceAllocation0[res2.resourceName])
	as.Equal(float64(res1.resourceQuantity.Value()), resourceAllocation0[res1.resourceName].AllocatedQuantity)
	as.Equal(float64(res2.resourceQuantity.Value()), resourceAllocation0[res2.resourceName].AllocatedQuantity)
	// res1 req 2 -> 1
	testPods[0].Spec.Containers[0].Resources.Requests[v1.ResourceName(res1.resourceName)] = *resource.NewQuantity(int64(1), resource.DecimalSI)
	err = testManager.Allocate(testPods[0], &testPods[0].Spec.Containers[0])
	as.Nil(err)
	res1Allocation := testManager.podResources.containerResource(string(testPods[0].UID), testPods[0].Spec.Containers[0].Name, res1.resourceName)
	as.NotNil(res1Allocation)
	// result is still 2
	as.Equal(float64(res1.resourceQuantity.Value()), res1Allocation.AllocatedQuantity)

	// container resource allocated < need
	err = testManager.Allocate(testPods[2], &testPods[2].Spec.Containers[0])
	as.Nil(err)
	resourceAllocation2 := testManager.podResources.containerResource(string(testPods[2].UID), testPods[2].Spec.Containers[0].Name, res2.resourceName)
	as.Equal(float64(res2.resourceQuantity.Value()), resourceAllocation2.AllocatedQuantity)
	// res2 req 2 -> 3
	testPods[2].Spec.Containers[0].Resources.Requests[v1.ResourceName(res2.resourceName)] = *resource.NewQuantity(int64(3), resource.DecimalSI)
	err = testManager.Allocate(testPods[2], &testPods[2].Spec.Containers[0])
	as.Nil(err)
	resourceAllocation2 = testManager.podResources.containerResource(string(testPods[2].UID), testPods[2].Spec.Containers[0].Name, res2.resourceName)
	as.Equal(float64(3), resourceAllocation2.AllocatedQuantity)

	// test skip endpoint error
	err = testManager.Allocate(testPods[3], &testPods[3].Spec.Containers[0])
	as.Nil(err)

	// test not skipping endpoint error
	setPodAnnotation(testPods[4], pluginapi.PodTypeAnnotationKey, pluginapi.PodTypeBestEffort)
	err = testManager.Allocate(testPods[4], &testPods[4].Spec.Containers[0])
	as.NotNil(err)
}

func setPodAnnotation(pod *v1.Pod, key, value string) {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}

	pod.Annotations[key] = value
}

func setPodLabel(pod *v1.Pod, key, value string) {
	if pod.Annotations == nil {
		pod.Labels = make(map[string]string)
	}

	pod.Labels[key] = value
}

func TestReconcileState(t *testing.T) {
	as := require.New(t)

	res1 := TestResource{
		resourceName:     "domain1.com/resource1",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}
	testResources := []TestResource{
		res1,
	}

	testPods := []*v1.Pod{
		makePod("Pod0", v1.ResourceList{
			v1.ResourceName(res1.resourceName): res1.resourceQuantity}),
	}
	podsStub := activePodsStub{
		activePods: testPods,
	}

	tmpDir, err := ioutil.TempDir("", "checkpoint")
	as.Nil(err)
	defer os.RemoveAll(tmpDir)
	testManager, err := getTestManager(tmpDir, podsStub.getActivePods, testResources)
	as.Nil(err)
	err = testManager.Allocate(testPods[0], &testPods[0].Spec.Containers[0])
	as.Nil(err)
	resp := testManager.podResources.containerResource(string(testPods[0].UID), testPods[0].Spec.Containers[0].Name, res1.resourceName)
	as.NotNil(resp)
	as.Equal(float64(res1.resourceQuantity.Value()), resp.AllocatedQuantity)

	rs := &apitest.FakeRuntimeService{}
	mockStatus := getMockStatusWithPods(testPods)
	testManager.containerRuntime = rs
	testManager.podStatusProvider = mockStatus

	// override resourceAlloc to reply to reconcile
	testManager.registerEndpoint(res1.resourceName, &pluginapi.ResourcePluginOptions{
		NeedReconcile: true,
	}, &MockEndpoint{
		resourceAlloc: func(ctx context.Context, request *pluginapi.GetResourcesAllocationRequest) (*pluginapi.GetResourcesAllocationResponse, error) {
			resp := new(pluginapi.GetResourcesAllocationResponse)
			resp.PodResources = make(map[string]*pluginapi.ContainerResources)
			resp.PodResources[string(testPods[0].UID)] = new(pluginapi.ContainerResources)
			resp.PodResources[string(testPods[0].UID)].ContainerResources = make(map[string]*pluginapi.ResourceAllocation)
			resp.PodResources[string(testPods[0].UID)].ContainerResources[testPods[0].Spec.Containers[0].Name] = new(pluginapi.ResourceAllocation)
			resp.PodResources[string(testPods[0].UID)].ContainerResources[testPods[0].Spec.Containers[0].Name].ResourceAllocation = make(map[string]*pluginapi.ResourceAllocationInfo)
			resp.PodResources[string(testPods[0].UID)].ContainerResources[testPods[0].Spec.Containers[0].Name].ResourceAllocation[res1.resourceName] = new(pluginapi.ResourceAllocationInfo)
			resp.PodResources[string(testPods[0].UID)].ContainerResources[testPods[0].Spec.Containers[0].Name].ResourceAllocation[res1.resourceName].IsNodeResource = true
			resp.PodResources[string(testPods[0].UID)].ContainerResources[testPods[0].Spec.Containers[0].Name].ResourceAllocation[res1.resourceName].IsScalarResource = true
			resp.PodResources[string(testPods[0].UID)].ContainerResources[testPods[0].Spec.Containers[0].Name].ResourceAllocation[res1.resourceName].AllocatedQuantity = 1
			resp.PodResources[string(testPods[0].UID)].ContainerResources[testPods[0].Spec.Containers[0].Name].ResourceAllocation[res1.resourceName].Envs = make(map[string]string)
			resp.PodResources[string(testPods[0].UID)].ContainerResources[testPods[0].Spec.Containers[0].Name].ResourceAllocation[res1.resourceName].Envs["kk1"] = "vv1"
			resp.PodResources[string(testPods[0].UID)].ContainerResources[testPods[0].Spec.Containers[0].Name].ResourceAllocation[res1.resourceName].Annotations = make(map[string]string)
			resp.PodResources[string(testPods[0].UID)].ContainerResources[testPods[0].Spec.Containers[0].Name].ResourceAllocation[res1.resourceName].Annotations["A1"] = "AA1"
			return resp, nil
		},
		allocateFunc: allocateStubFunc(),
	})

	testManager.reconcileState()

	// ensures containerResource matches with reconcile result and runtime UpdateContainerResources must be called
	resp = testManager.podResources.containerResource(string(testPods[0].UID), testPods[0].Spec.Containers[0].Name, res1.resourceName)
	as.NotNil(resp)
	as.Equal(float64(1), resp.AllocatedQuantity)
	err = rs.AssertCalls([]string{"UpdateContainerResources"})
	as.Nil(err)

	// ensures that reconcile do allocation for active pods without allocatation results
	pod1 := makePod("Pod1", v1.ResourceList{v1.ResourceName(res1.resourceName): res1.resourceQuantity})
	testPods = append(testPods, pod1)
	podsStub.activePods = testPods
	mockStatus = getMockStatusWithPods(testPods)
	testManager.podStatusProvider = mockStatus
	testManager.activePods = podsStub.getActivePods

	resp = testManager.podResources.containerResource(string(pod1.UID), pod1.Spec.Containers[0].Name, res1.resourceName)
	as.Nil(resp)

	testManager.reconcileState()

	resp = testManager.podResources.containerResource(string(pod1.UID), pod1.Spec.Containers[0].Name, res1.resourceName)
	as.NotNil(resp)
	as.Equal(float64(res1.resourceQuantity.Value()), resp.AllocatedQuantity)
}

func resourceStubAlloc() func(ctx context.Context, request *pluginapi.GetResourcesAllocationRequest) (*pluginapi.GetResourcesAllocationResponse, error) {
	return func(ctx context.Context, request *pluginapi.GetResourcesAllocationRequest) (*pluginapi.GetResourcesAllocationResponse, error) {
		resp := new(pluginapi.GetResourcesAllocationResponse)
		resp.PodResources = make(map[string]*pluginapi.ContainerResources)
		resp.PodResources[string(types.UID("Pod1"))] = new(pluginapi.ContainerResources)
		resp.PodResources[string(types.UID("Pod1"))].ContainerResources = make(map[string]*pluginapi.ResourceAllocation)
		resp.PodResources[string(types.UID("Pod1"))].ContainerResources["Cont0"] = new(pluginapi.ResourceAllocation)
		resp.PodResources[string(types.UID("Pod1"))].ContainerResources["Cont0"].ResourceAllocation = make(map[string]*pluginapi.ResourceAllocationInfo)
		resp.PodResources[string(types.UID("Pod1"))].ContainerResources["Cont0"].ResourceAllocation["domain1.com/resource1"] = new(pluginapi.ResourceAllocationInfo)
		resp.PodResources[string(types.UID("Pod1"))].ContainerResources["Cont0"].ResourceAllocation["domain1.com/resource1"].IsNodeResource = true
		resp.PodResources[string(types.UID("Pod1"))].ContainerResources["Cont0"].ResourceAllocation["domain1.com/resource1"].IsScalarResource = true
		resp.PodResources[string(types.UID("Pod1"))].ContainerResources["Cont0"].ResourceAllocation["domain1.com/resource1"].AllocatedQuantity = 1
		resp.PodResources[string(types.UID("Pod1"))].ContainerResources["Cont0"].ResourceAllocation["domain1.com/resource1"].Envs = make(map[string]string)
		resp.PodResources[string(types.UID("Pod1"))].ContainerResources["Cont0"].ResourceAllocation["domain1.com/resource1"].Envs["kk1"] = "vv1"
		resp.PodResources[string(types.UID("Pod1"))].ContainerResources["Cont0"].ResourceAllocation["domain1.com/resource1"].Annotations = make(map[string]string)
		resp.PodResources[string(types.UID("Pod1"))].ContainerResources["Cont0"].ResourceAllocation["domain1.com/resource1"].Annotations["A1"] = "AA1"
		return resp, nil
	}
}

func TestDeRegisterPlugin(t *testing.T) {
	as := require.New(t)

	res1 := TestResource{
		resourceName:     "domain1.com/resource1",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}

	testResources := []TestResource{
		res1,
	}

	podsStub := activePodsStub{
		activePods: []*v1.Pod{},
	}
	tmpDir, err := ioutil.TempDir("/tmp", "checkpoint")
	as.Nil(err)
	defer os.RemoveAll(tmpDir)

	testManager, err := getTestManager(tmpDir, podsStub.getActivePods, testResources)
	as.Nil(err)

	_, exists := testManager.endpoints[res1.resourceName]
	as.True(exists)

	as.NotNil(testManager.endpoints[res1.resourceName].e)
	as.False(testManager.endpoints[res1.resourceName].e.isStopped())

	testManager.DeRegisterPlugin(res1.resourceName)

	_, exists = testManager.endpoints[res1.resourceName]
	as.True(exists)

	// ensures DeRegisterPlugin worked
	as.NotNil(testManager.endpoints[res1.resourceName].e)
	as.True(testManager.endpoints[res1.resourceName].e.isStopped())
}

func TestUtils(t *testing.T) {
	as := require.New(t)

	//GetContainerTypeAndIndex
	pod := &v1.Pod{}
	pod.UID = types.UID("Pod1")
	cont0 := v1.Container{}
	cont0.Name = "InitContName0"
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, cont0)
	contType, contIndex, err := GetContainerTypeAndIndex(pod, &cont0)
	as.Nil(err)
	as.Equal(pluginapi.ContainerType_INIT, contType)
	as.Equal(uint64(0x0), contIndex)
	cont1 := v1.Container{}
	cont1.Name = "InitContName1"
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, cont1)
	contType, contIndex, err = GetContainerTypeAndIndex(pod, &cont1)
	as.Nil(err)
	as.Equal(pluginapi.ContainerType_INIT, contType)
	as.Equal(uint64(0x1), contIndex)

	cont2 := v1.Container{}
	cont2.Name = "AppCont0"
	pod.Spec.Containers = append(pod.Spec.Containers, cont2)
	contType, contIndex, err = GetContainerTypeAndIndex(pod, &cont2)
	as.Nil(err)
	as.Equal(pluginapi.ContainerType_MAIN, contType)
	as.Equal(uint64(0x0), contIndex)

	cont3 := v1.Container{}
	cont3.Name = "AppCont1"
	pod.Spec.Containers = append(pod.Spec.Containers, cont3)
	contType, contIndex, err = GetContainerTypeAndIndex(pod, &cont3)
	as.Nil(err)
	as.Equal(pluginapi.ContainerType_SIDECAR, contType)
	as.Equal(uint64(0x1), contIndex)

	//GetContextWithSpecificInfo
	pod1 := &v1.Pod{}
	pod1.UID = types.UID("TestPod")
	pod1.Name = "PodName"
	cont4 := v1.Container{}
	cont4.Name = "Cont4"
	pod1.Spec.Containers = append(pod1.Spec.Containers, cont4)
	_, err = GetContextWithSpecificInfo(pod1, &pod1.Spec.Containers[0])
	as.Nil(err)

	setPodLabel(pod1, PSMLabelKey, "Value1")
	Context2, err := GetContextWithSpecificInfo(pod1, &pod1.Spec.Containers[0])
	as.Equal(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("psm", "Value1")), Context2)
}

func TestUpdateAllocatedResource(t *testing.T) {
	as := require.New(t)

	res1 := TestResource{
		resourceName:     "domain1.com/resource1",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}
	res2 := TestResource{
		resourceName:     "domain2.com/resource2",
		resourceQuantity: *resource.NewQuantity(int64(1), resource.DecimalSI),
	}
	testResources := []TestResource{
		res1,
		res2,
	}

	pod1 := makePod("Pod1", v1.ResourceList{
		v1.ResourceName(res1.resourceName): res1.resourceQuantity})
	pod2 := makePod("Pod2", v1.ResourceList{
		v1.ResourceName(res2.resourceName): res2.resourceQuantity})

	testPods := []*v1.Pod{
		pod1,
		pod2,
	}

	podsStub := activePodsStub{
		activePods: testPods,
	}
	tmpDir, err := ioutil.TempDir("", "checkpoint")
	as.Nil(err)
	defer os.RemoveAll(tmpDir)
	testManager, err := getTestManager(tmpDir, podsStub.getActivePods, testResources)
	as.Nil(err)

	err = testManager.Allocate(pod1, &pod1.Spec.Containers[0])
	as.Nil(err)
	resourceAllocation1 := testManager.podResources.containerResource(string(pod1.UID), pod1.Spec.Containers[0].Name, res1.resourceName)
	as.NotNil(resourceAllocation1)
	as.Equal(float64(res1.resourceQuantity.Value()), resourceAllocation1.AllocatedQuantity)

	err = testManager.Allocate(pod2, &pod2.Spec.Containers[0])
	as.Nil(err)
	resourceAllocation2 := testManager.podResources.containerResource(string(pod2.UID), pod2.Spec.Containers[0].Name, res2.resourceName)
	as.NotNil(resourceAllocation2)
	as.Equal(float64(res2.resourceQuantity.Value()), resourceAllocation2.AllocatedQuantity)

	podsStub1 := activePodsStub{
		activePods: []*v1.Pod{pod1},
	}
	testManager.activePods = podsStub1.getActivePods
	testManager.UpdateAllocatedResources()

	// ensures that resourceAllocation1 still exists
	resourceAllocation1 = testManager.podResources.containerResource(string(pod1.UID), pod1.Spec.Containers[0].Name, res1.resourceName)
	as.NotNil(resourceAllocation1)
	as.Equal(float64(res1.resourceQuantity.Value()), resourceAllocation1.AllocatedQuantity)

	// ensures that resourceAllocation2 was removed
	resourceAllocation2 = testManager.podResources.containerResource(string(pod2.UID), pod2.Spec.Containers[0].Name, res2.resourceName)
	as.Nil(resourceAllocation2)
}

func TestCheckPoint(t *testing.T) {
	as := require.New(t)

	res1 := TestResource{
		resourceName:     "domain1.com/resource1",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}
	res2 := TestResource{
		resourceName:     "domain2.com/resource2",
		resourceQuantity: *resource.NewQuantity(int64(1), resource.DecimalSI),
	}

	testResources := make([]TestResource, 0, 2)
	testResources = append(testResources, res1)
	testResources = append(testResources, res2)

	testPods := []*v1.Pod{
		makePod("Pod0", v1.ResourceList{
			v1.ResourceName(res1.resourceName): res1.resourceQuantity}),
		makePod("Pod1", v1.ResourceList{
			v1.ResourceName(res2.resourceName): res2.resourceQuantity}),
	}

	podsStub := activePodsStub{
		activePods: testPods,
	}
	tmpDir, err := ioutil.TempDir("/tmp", "checkpoint")
	as.Nil(err)
	defer os.RemoveAll(tmpDir)

	testManager, err := getTestManager(tmpDir, podsStub.getActivePods, testResources)
	as.Nil(err)

	path := filepath.Join(testManager.socketdir, kubeletQoSResourceManagerCheckpoint)
	_, err = os.Create(path)
	as.Nil(err)

	// Before qrm start
	err = os.Remove(path)
	as.Nil(err)

	mockStatus := getMockStatusWithPods(testPods)
	err = testManager.Start(podsStub.getActivePods, &sourcesReadyStub{}, mockStatus, &apitest.FakeRuntimeService{})
	as.Nil(err)

	err = testManager.Allocate(testPods[0], &testPods[0].Spec.Containers[0])
	as.Nil(err)
	_, err = os.Stat(path)
	// ensures that checkpoint exists after allocation
	as.Nil(err)

	//After qrm start
	err = testManager.Stop()
	as.Nil(err)

	mockStatus = getMockStatusWithPods(testPods)
	err = testManager.Start(podsStub.getActivePods, &sourcesReadyStub{}, mockStatus, &apitest.FakeRuntimeService{})
	as.Nil(err)

	testManager.registerEndpoint("domain1.com/resource1", new(pluginapi.ResourcePluginOptions), &MockEndpoint{allocateFunc: allocateStubFunc()})
	testManager.registerEndpoint("domain2.com/resource2", &pluginapi.ResourcePluginOptions{
		NeedReconcile: true,
	}, &MockEndpoint{
		allocateFunc: func(req *pluginapi.ResourceRequest) (*pluginapi.ResourceAllocationResponse, error) {
			resp := new(pluginapi.ResourceAllocationResponse)
			resp.AllocationResult = new(pluginapi.ResourceAllocation)
			resp.AllocationResult.ResourceAllocation = make(map[string]*pluginapi.ResourceAllocationInfo)
			resp.AllocationResult.ResourceAllocation["domain2.com/resource2"] = new(pluginapi.ResourceAllocationInfo)
			resp.AllocationResult.ResourceAllocation["domain2.com/resource2"].Envs = make(map[string]string)
			resp.AllocationResult.ResourceAllocation["domain2.com/resource2"].Envs["key2"] = "val2"
			resp.AllocationResult.ResourceAllocation["domain2.com/resource2"].IsScalarResource = true
			resp.AllocationResult.ResourceAllocation["domain2.com/resource2"].IsNodeResource = true
			resp.AllocationResult.ResourceAllocation["domain2.com/resource2"].AllocatedQuantity = 1
			resp.AllocationResult.ResourceAllocation["domain2.com/resource2"].AllocationResult = "0"
			return resp, nil
		},
	})

	err = os.Remove(path)
	as.Nil(err)

	err = testManager.Allocate(testPods[1], &testPods[1].Spec.Containers[0])
	as.Nil(err)

	_, err = os.Stat(path)
	as.Nil(err)

	err = os.Remove(path)
	as.Nil(err)

	resp := testManager.podResources.containerAllResources(string(testPods[1].UID), testPods[1].Spec.Containers[0].Name)
	as.NotNil(resp)

	// ensures checkpoint matches with result from allocateFunc after remove checkpoint file
	as.Equal(resp[res2.resourceName].AllocationResult, "0")

	testManager.reconcileState()

	// ensures that chk resumes after reconcile
	_, err = os.Stat(path)
	as.Nil(err)

	resp = testManager.podResources.containerAllResources(string(testPods[1].UID), testPods[1].Spec.Containers[0].Name)
	as.NotNil(resp)

	// ensures checkpoint matches with result from allocateFunc after checkpoint file resumes
	as.Equal(resp[res2.resourceName].AllocationResult, "0")
}

func TestGetResourceRunContainerOptions(t *testing.T) {
	as := require.New(t)

	res1 := TestResource{
		resourceName:     "domain1.com/resource1",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}

	res2 := TestResource{
		resourceName:     "domain2.com/resource2",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}

	pod1 := makePod("Pod1", v1.ResourceList{
		v1.ResourceName(res1.resourceName): res1.resourceQuantity})

	pod2 := makePod("Pod2", v1.ResourceList{
		v1.ResourceName(res2.resourceName): res2.resourceQuantity})

	podsStub := activePodsStub{
		activePods: []*v1.Pod{pod1, pod2},
	}

	tmpDir, err := ioutil.TempDir("", "checkpoint")
	as.Nil(err)
	defer os.RemoveAll(tmpDir)

	testManager, err := getTestManager(tmpDir, podsStub.getActivePods, []TestResource{res1, res2})
	as.Nil(err)

	err = testManager.Allocate(pod1, &pod1.Spec.Containers[0])
	as.Nil(err)

	err = testManager.Allocate(pod2, &pod2.Spec.Containers[0])
	as.Nil(err)

	runContainerOpts1, err := testManager.GetResourceRunContainerOptions(pod1, &pod1.Spec.Containers[0])
	as.Nil(err)
	as.NotNil(runContainerOpts1.Resources)

	as.Equal(pod1.Annotations[PreStartAnnotationKey], PreStartAnnotationValue)

	runContainerOpts2, err := testManager.GetResourceRunContainerOptions(pod2, &pod2.Spec.Containers[0])
	as.Nil(err)
	as.NotNil(runContainerOpts2.Resources)

	// ensures prestart has been called
	as.Equal(pod2.Annotations[PreStartAnnotationKey], PreStartAnnotationValue)

	// ensures AllocationResult and OCI result in chk and runContainerOpt are equal
	resp1 := testManager.podResources.containerAllResources(string(pod1.UID), pod1.Spec.Containers[0].Name)
	as.NotNil(resp1)
	as.NotNil(resp1[res1.resourceName])

	resp2 := testManager.podResources.containerAllResources(string(pod2.UID), pod2.Spec.Containers[0].Name)
	as.NotNil(resp2)
	as.NotNil(resp2[res2.resourceName])

	as.Equal(resp1[res1.resourceName].AllocationResult, runContainerOpts1.Resources.GetCpusetCpus())
	as.Equal(resp2[res2.resourceName].AllocationResult, runContainerOpts2.Resources.GetCpusetMems())

	// ensures annotations and envs in chk and runContainerOpt are equal
	as.Condition(func() bool {
		return annotationsEqual(resp1[res1.resourceName].Annotations, runContainerOpts1.Annotations)
	})
	as.Condition(func() bool {
		return annotationsEqual(resp2[res2.resourceName].Annotations, runContainerOpts2.Annotations)
	})
	as.Condition(func() bool {
		return envsEqual(resp1[res1.resourceName].Envs, runContainerOpts1.Envs)
	})
	as.Condition(func() bool {
		return envsEqual(resp2[res2.resourceName].Envs, runContainerOpts2.Envs)
	})
}

func envsEqual(env1 map[string]string, env2 []container.EnvVar) bool {
	if len(env1) != len(env2) {
		return false
	}

	for _, env := range env2 {
		if val, found := env1[env.Name]; !found || val != env.Value {
			return false
		}
	}

	return true
}

func annotationsEqual(anno1 map[string]string, anno2 []container.Annotation) bool {
	if len(anno1) != len(anno2) {
		return false
	}

	for _, anno := range anno2 {
		if val, found := anno1[anno.Name]; !found || val != anno.Value {
			return false
		}
	}

	return true
}

var flag int = 0

func topologyStubAllocatable() func(ctx context.Context, request *pluginapi.GetTopologyAwareAllocatableResourcesRequest) (*pluginapi.GetTopologyAwareAllocatableResourcesResponse, error) {
	return func(ctx context.Context, request *pluginapi.GetTopologyAwareAllocatableResourcesRequest) (*pluginapi.GetTopologyAwareAllocatableResourcesResponse, error) {
		resp := new(pluginapi.GetTopologyAwareAllocatableResourcesResponse)
		resp.AllocatableResources = make(map[string]*pluginapi.AllocatableTopologyAwareResource)
		if flag == 1 {
			resp.AllocatableResources["domain1.com/resource1"] = new(pluginapi.AllocatableTopologyAwareResource)
			resp.AllocatableResources["domain1.com/resource1"].IsNodeResource = true
			resp.AllocatableResources["domain1.com/resource1"].IsScalarResource = true
			resp.AllocatableResources["domain1.com/resource1"].AggregatedQuantity = 3
		}
		if flag == 2 {
			resp.AllocatableResources["domain1.com/resource1"] = new(pluginapi.AllocatableTopologyAwareResource)
			resp.AllocatableResources["domain1.com/resource1"].IsNodeResource = true
			resp.AllocatableResources["domain1.com/resource1"].IsScalarResource = true
			resp.AllocatableResources["domain1.com/resource1"].AggregatedQuantity = 3
			resp.AllocatableResources["domain2.com/resource2"] = new(pluginapi.AllocatableTopologyAwareResource)
			resp.AllocatableResources["domain2.com/resource2"].IsNodeResource = true
			resp.AllocatableResources["domain2.com/resource2"].IsScalarResource = true
			resp.AllocatableResources["domain2.com/resource2"].AggregatedQuantity = 4
		}
		if flag == 3 {
			resp.AllocatableResources["domain1.com/resource1"] = new(pluginapi.AllocatableTopologyAwareResource)
			resp.AllocatableResources["domain1.com/resource1"].IsNodeResource = false
			resp.AllocatableResources["domain1.com/resource1"].IsScalarResource = true
			resp.AllocatableResources["domain1.com/resource1"].AggregatedQuantity = 4
			resp.AllocatableResources["domain1.com/resource1"].TopologyAwareQuantityList = []*pluginapi.TopologyAwareQuantity{
				{ResourceValue: "0-1", Node: 0},
				{ResourceValue: "2-3", Node: 1},
			}
		}
		if flag == 4 {
			return resp, fmt.Errorf("GetTopologyAwareAllocatableResourcesResponse failed")
		}
		//fmt.Printf("%f\n", resp.AllocatableResources.TopologyAwareResources["domain1.com/resource1"].AggregatedQuantity)
		return resp, nil
	}

}

func TestGetCapacity(t *testing.T) {
	as := require.New(t)

	res1 := TestResource{
		resourceName:     "domain1.com/resource1",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}
	res2 := TestResource{
		resourceName:     "domain2.com/resource2",
		resourceQuantity: *resource.NewQuantity(int64(1), resource.DecimalSI),
	}

	testResources := []TestResource{
		res1,
		res2,
	}

	podsStub := activePodsStub{
		activePods: []*v1.Pod{},
	}
	tmpDir, err := ioutil.TempDir("", "checkpoint")
	as.Nil(err)
	defer os.RemoveAll(tmpDir)
	testManager, err := getTestManager(tmpDir, podsStub.getActivePods, testResources)
	as.Nil(err)

	//add 3 resource1
	//Stops resource1 endpoint.
	flag = 1
	SGP = 1
	testManager.endpoints[res1.resourceName] = endpointInfo{
		e: &MockEndpoint{topologyAllocatable: topologyStubAllocatable()},
	}
	testManager.endpoints["cpu"] = endpointInfo{
		e: &MockEndpoint{},
	}
	_, allocatable, deletedResourcesName := testManager.GetCapacity()
	as.Equal(v1.ResourceList{}, allocatable)
	ds := sets.NewString(deletedResourcesName...)
	as.True(ds.Has(res1.resourceName))
	as.True(ds.Has(res2.resourceName))
	// not k8s scalar resource, so ensures ignoring
	as.False(ds.Has("cpu"))
	//
	SGP = 0
	testManager.endpoints[res1.resourceName] = endpointInfo{
		e: &MockEndpoint{topologyAllocatable: topologyStubAllocatable()},
	}
	capacity, allocatable, _ := testManager.GetCapacity()
	ExpectC, _ := resource.ParseQuantity(fmt.Sprintf("%.3f", float64(3)))
	ExpectA, _ := resource.ParseQuantity(fmt.Sprintf("%.3f", float64(3)))
	as.Equal(ExpectC, capacity["domain1.com/resource1"])
	as.Equal(ExpectA, allocatable["domain1.com/resource1"])

	//add 4 resource2
	flag = 2
	testManager.endpoints[res2.resourceName] = endpointInfo{
		e: &MockEndpoint{topologyAllocatable: topologyStubAllocatable()},
	}
	capacity, allocatable, _ = testManager.GetCapacity()
	ExpectC, _ = resource.ParseQuantity(fmt.Sprintf("%.3f", float64(4)))
	ExpectA, _ = resource.ParseQuantity(fmt.Sprintf("%.3f", float64(4)))
	as.Equal(ExpectC, capacity["domain2.com/resource2"])
	as.Equal(ExpectA, allocatable["domain2.com/resource2"])
}

func TestGetTopologyAwareAllocatableResources(t *testing.T) {
	as := require.New(t)

	podsStub := activePodsStub{
		activePods: []*v1.Pod{},
	}
	tmpDir, err := ioutil.TempDir("/tmp", "checkpoint")
	as.Nil(err)
	defer os.RemoveAll(tmpDir)

	testManager, err := getTestManager(tmpDir, podsStub.getActivePods, nil)
	as.Nil(err)

	testManager.registerEndpoint("domain1.com/resource1", &pluginapi.ResourcePluginOptions{
		PreStartRequired:      true,
		WithTopologyAlignment: true,
		NeedReconcile:         true,
	}, &MockEndpoint{topologyAllocatable: topologyStubAllocatable()})

	flag = 3
	resp, err := testManager.GetTopologyAwareAllocatableResources()
	as.Nil(err)
	as.NotNil(resp.AllocatableResources)
	as.NotNil(resp.AllocatableResources["domain1.com/resource1"])
	as.Equal(resp.AllocatableResources["domain1.com/resource1"], &pluginapi.AllocatableTopologyAwareResource{
		IsNodeResource:     false,
		IsScalarResource:   true,
		AggregatedQuantity: 4,
		TopologyAwareQuantityList: []*pluginapi.TopologyAwareQuantity{
			{ResourceValue: "0-1", Node: 0},
			{ResourceValue: "2-3", Node: 1},
		},
	})

	flag = 4
	resp, err = testManager.GetTopologyAwareAllocatableResources()
	as.NotNil(err)
}

func TestGetTopologyAwareResources(t *testing.T) {
	as := require.New(t)

	res1 := TestResource{
		resourceName:     "domain1.com/resource1",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}

	res2 := TestResource{
		resourceName:     "domain2.com/resource2",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}

	pod1 := makePod("Pod1", v1.ResourceList{
		v1.ResourceName(res1.resourceName): res1.resourceQuantity})

	pod2 := makePod("Pod2", v1.ResourceList{
		v1.ResourceName(res2.resourceName): res2.resourceQuantity})

	podsStub := activePodsStub{
		activePods: []*v1.Pod{pod1, pod2},
	}

	tmpDir, err := ioutil.TempDir("", "checkpoint")
	as.Nil(err)
	defer os.RemoveAll(tmpDir)

	testManager, err := getTestManager(tmpDir, podsStub.getActivePods, []TestResource{res1, res2})
	as.Nil(err)

	err = testManager.Allocate(pod1, &pod1.Spec.Containers[0])
	as.Nil(err)
	res1Allocation := testManager.podResources.containerResource(string(pod1.UID), pod1.Spec.Containers[0].Name, res1.resourceName)
	as.NotNil(res1Allocation)
	as.Equal(float64(res1.resourceQuantity.Value()), res1Allocation.AllocatedQuantity)

	err = testManager.Allocate(pod2, &pod2.Spec.Containers[0])
	as.Nil(err)
	res2Allocation := testManager.podResources.containerResource(string(pod2.UID), pod2.Spec.Containers[0].Name, res2.resourceName)
	as.NotNil(res2Allocation)
	as.Equal(float64(res2.resourceQuantity.Value()), res2Allocation.AllocatedQuantity)

	pod1Resp := &pluginapi.GetTopologyAwareResourcesResponse{
		PodUid:       string(pod1.UID),
		PodName:      pod1.Name,
		PodNamespace: pod1.Namespace,
		ContainerTopologyAwareResources: &pluginapi.ContainerTopologyAwareResources{
			ContainerName: pod1.Spec.Containers[0].Name,
			AllocatedResources: map[string]*pluginapi.TopologyAwareResource{
				res1.resourceName: {
					IsNodeResource:     res1Allocation.IsNodeResource,
					IsScalarResource:   res1Allocation.IsScalarResource,
					AggregatedQuantity: res1Allocation.AllocatedQuantity,
					TopologyAwareQuantityList: []*pluginapi.TopologyAwareQuantity{
						{ResourceValue: res1Allocation.AllocationResult, Node: 0},
					},
				},
			},
		},
	}

	pod2Resp := &pluginapi.GetTopologyAwareResourcesResponse{
		PodUid:       string(pod2.UID),
		PodName:      pod2.Name,
		PodNamespace: pod2.Namespace,
		ContainerTopologyAwareResources: &pluginapi.ContainerTopologyAwareResources{
			ContainerName: pod2.Spec.Containers[0].Name,
			AllocatedResources: map[string]*pluginapi.TopologyAwareResource{
				res2.resourceName: {
					IsNodeResource:     res2Allocation.IsNodeResource,
					IsScalarResource:   res2Allocation.IsScalarResource,
					AggregatedQuantity: res2Allocation.AllocatedQuantity,
					TopologyAwareQuantityList: []*pluginapi.TopologyAwareQuantity{
						{ResourceValue: res2Allocation.AllocationResult, Node: 1},
					},
				},
			},
		},
	}

	testManager.registerEndpoint(res1.resourceName, &pluginapi.ResourcePluginOptions{
		PreStartRequired:      true,
		WithTopologyAlignment: true,
		NeedReconcile:         true,
	}, &MockEndpoint{topologyAllocated: func(ctx context.Context, request *pluginapi.GetTopologyAwareResourcesRequest) (*pluginapi.GetTopologyAwareResourcesResponse, error) {
		if request == nil {
			return nil, fmt.Errorf("getTopologyAwareResources got nil request")
		}

		if request.PodUid == string(pod1.UID) && request.ContainerName == pod1.Spec.Containers[0].Name {
			return proto.Clone(pod1Resp).(*pluginapi.GetTopologyAwareResourcesResponse), nil
		}

		return nil, nil
	}})

	testManager.registerEndpoint(res2.resourceName, &pluginapi.ResourcePluginOptions{
		PreStartRequired:      true,
		WithTopologyAlignment: true,
		NeedReconcile:         true,
	}, &MockEndpoint{topologyAllocated: func(ctx context.Context, request *pluginapi.GetTopologyAwareResourcesRequest) (*pluginapi.GetTopologyAwareResourcesResponse, error) {
		if request == nil {
			return nil, fmt.Errorf("getTopologyAwareResources got nil request")
		}

		if request.PodUid == string(pod2.UID) && request.ContainerName == pod2.Spec.Containers[0].Name {
			return proto.Clone(pod2Resp).(*pluginapi.GetTopologyAwareResourcesResponse), nil
		}

		return nil, nil
	}})

	resp1, err := testManager.GetTopologyAwareResources(pod1, &pod1.Spec.Containers[0])
	as.Nil(err)
	as.Equal(resp1, pod1Resp)

	resp2, err := testManager.GetTopologyAwareResources(pod2, &pod2.Spec.Containers[0])
	as.Nil(err)
	as.Equal(resp2, pod2Resp)
}

func TestUpdatePluginResources(t *testing.T) {
	as := require.New(t)

	res1 := TestResource{
		resourceName:     "domain1.com/resource1",
		resourceQuantity: *resource.NewQuantity(int64(2), resource.DecimalSI),
	}

	testResources := []TestResource{
		res1,
	}

	pod1 := makePod("Pod1", v1.ResourceList{
		v1.ResourceName(res1.resourceName): res1.resourceQuantity})

	testPods := []*v1.Pod{
		pod1,
	}

	podsStub := activePodsStub{
		activePods: testPods,
	}

	tmpDir, err := ioutil.TempDir("/tmp", "checkpoint")
	as.Nil(err)
	defer os.RemoveAll(tmpDir)

	testManager, err := getTestManager(tmpDir, podsStub.getActivePods, testResources)
	as.Nil(err)

	err = testManager.Allocate(pod1, &pod1.Spec.Containers[0])
	as.Nil(err)

	cachedNode := &v1.Node{}
	nodeInfo := &schedulerframework.NodeInfo{}
	nodeInfo.SetNode(cachedNode)

	testManager.UpdatePluginResources(nodeInfo, &lifecycle.PodAdmitAttributes{Pod: pod1})
	allocatableResource := nodeInfo.AllocatableResource()
	as.NotNil(allocatableResource)
	as.Equal(res1.resourceQuantity.Value(), allocatableResource.ScalarResources[v1.ResourceName(res1.resourceName)])
}

func constructResourceAlloc(OciPropertyName string, IsNodeResource bool, IsScalarResource bool) *pluginapi.ResourceAllocationInfo {
	resp := &pluginapi.ResourceAllocationInfo{}
	resp.OciPropertyName = OciPropertyName
	resp.IsNodeResource = IsNodeResource
	resp.IsScalarResource = IsScalarResource
	return resp
}

/*
func TestResetExtendedResource(t *testing.T) {
	as := assert.New(t)
	tmpDir, err := ioutil.TempDir("", "checkpoint")
	as.Nil(err)
	ckm, err := checkpointmanager.NewCheckpointManager(tmpDir)
	as.Nil(err)
	testManager := &ManagerImpl{
		endpoints:                        make(map[string]endpointInfo),
		allocatedScalarResourcesQuantity: make(map[string]float64),
		podResources:                     newPodResourcesChk(),
		checkpointManager:                ckm,
	}
	extendedResourceName := "domain.com/resource1"
	testManager.podResources.insert("pod", "con", extendedResourceName, constructResourceAlloc("Name", true, true))

	//checkpoint is present, indicating node hasn't been recreated
	err = testManager.writeCheckpoint()
	as.Nil(err)
	as.False(testManager.ShouldResetExtendedResourceCapacity())

	//checkpoint is absent, representing node recreation
	ckpts, err := ckm.ListCheckpoints()
	as.Nil(err)
	for _, ckpt := range ckpts {
		err := ckm.RemoveCheckpoint(ckpt)
		as.Nil(err)
	}
	utilfeature.DefaultMutableFeatureGate.Set("QoSResourceManager=true")
	as.True(testManager.ShouldResetExtendedResourceCapacity())
}
*/
func setupManager(t *testing.T, socketName string) *ManagerImpl {
	topologyStore := topologymanager.NewFakeManager()
	m, err := newManagerImpl(socketName, nil, topologyStore, time.Second)
	require.NoError(t, err)

	activePods := func() []*v1.Pod {
		return []*v1.Pod{}
	}

	mockStatus := new(statustest.MockStatusProvider)

	err = m.Start(activePods, &sourcesReadyStub{}, mockStatus, &apitest.FakeRuntimeService{})
	require.NoError(t, err)

	return m
}

func setupPlugin(t *testing.T, pluginSocketName string) *Stub {
	p := NewResourcePluginStub(pluginSocketName, testResourceName, false)
	err := p.Start()
	require.NoError(t, err)
	return p
}

func setupPluginManager(t *testing.T, pluginSocketName string, m *ManagerImpl) (pluginmanager.PluginManager, chan struct{}) {
	pluginManager := pluginmanager.NewPluginManager(
		filepath.Dir(pluginSocketName), /* sockDir */
		&record.FakeRecorder{},
	)

	pluginManager.AddHandler(watcherapi.ResourcePlugin, m.GetWatcherHandler())

	stopCh := make(chan struct{})
	runPluginManager(pluginManager, stopCh)
	return pluginManager, stopCh
}

func runPluginManager(pluginManager pluginmanager.PluginManager, stopCh chan struct{}) {
	sourcesReady := config.NewSourcesReady(func(_ sets.String) bool { return true })
	go pluginManager.Run(sourcesReady, stopCh)
}

func setup(t *testing.T, socketName string, pluginSocketName string) (*ManagerImpl, *Stub) {
	m := setupManager(t, socketName)
	p := setupPlugin(t, pluginSocketName)
	return m, p
}

func setupInProbeMode(t *testing.T, socketName string, pluginSocketName string) (*ManagerImpl, *Stub, pluginmanager.PluginManager, chan struct{}) {
	m := setupManager(t, socketName)
	pm, stopCh := setupPluginManager(t, pluginSocketName, m)
	p := setupPlugin(t, pluginSocketName)
	return m, p, pm, stopCh
}

func cleanup(t *testing.T, m *ManagerImpl, p *Stub, stopCh chan struct{}) {
	p.Stop()
	m.Stop()

	if stopCh != nil {
		close(stopCh)
	}
}

func allocateStubFunc() func(*pluginapi.ResourceRequest) (*pluginapi.ResourceAllocationResponse, error) {
	return func(req *pluginapi.ResourceRequest) (*pluginapi.ResourceAllocationResponse, error) {
		resp := new(pluginapi.ResourceAllocationResponse)
		resp.ResourceName = "domain1.com/resource1"
		//resp.ContainerName = "Cont1"
		resp.AllocationResult = new(pluginapi.ResourceAllocation)
		resp.AllocationResult.ResourceAllocation = make(map[string]*pluginapi.ResourceAllocationInfo)
		resp.AllocationResult.ResourceAllocation["domain1.com/resource1"] = new(pluginapi.ResourceAllocationInfo)
		resp.AllocationResult.ResourceAllocation["domain1.com/resource1"].Envs = make(map[string]string)
		resp.AllocationResult.ResourceAllocation["domain1.com/resource1"].Envs["key1"] = "val1"
		resp.AllocationResult.ResourceAllocation["domain1.com/resource1"].IsScalarResource = true
		resp.AllocationResult.ResourceAllocation["domain1.com/resource1"].IsNodeResource = true
		resp.AllocationResult.ResourceAllocation["domain1.com/resource1"].AllocatedQuantity = 2
		return resp, nil
	}
}

func getMockStatusWithPods(pods []*v1.Pod) status.PodStatusProvider {
	mockStatus := &statustest.MockStatusProvider{}

	for _, pod := range pods {
		containerStatuses := make([]v1.ContainerStatus, 0, len(pod.Spec.Containers))

		for _, container := range pod.Spec.Containers {
			ContStat := v1.ContainerStatus{
				Name:        container.Name,
				ContainerID: fmt.Sprintf("ContId://%s", uuid.NewUUID()),
			}
			containerStatuses = append(containerStatuses, ContStat)
		}

		p0Time := metav1.Now()
		mockStatus.On("GetPodStatus", pod.UID).Return(v1.PodStatus{StartTime: &p0Time, ContainerStatuses: containerStatuses}, true)
	}

	return mockStatus
}

func registerEndpointByRes(manager *ManagerImpl, testRes []TestResource) error {
	if manager == nil {
		return fmt.Errorf("registerEndpointByRes got nil manager")
	}

	for i, res := range testRes {
		var OciPropertyName string
		if res.resourceName == "domain1.com/resource1" {
			OciPropertyName = "CpusetCpus"
		} else if res.resourceName == "domain2.com/resource2" {
			OciPropertyName = "CpusetMems"
		}

		curResourceName := res.resourceName

		if res.resourceName == "domain1.com/resource1" || res.resourceName == "domain2.com/resource2" {
			manager.registerEndpoint(curResourceName, &pluginapi.ResourcePluginOptions{
				PreStartRequired:      true,
				WithTopologyAlignment: true,
				NeedReconcile:         true,
			}, &MockEndpoint{
				allocateFunc: func(req *pluginapi.ResourceRequest) (*pluginapi.ResourceAllocationResponse, error) {
					if req == nil {
						return nil, fmt.Errorf("allocateFunc got nil request")
					}

					resp := new(pluginapi.ResourceAllocationResponse)
					resp.AllocationResult = new(pluginapi.ResourceAllocation)
					resp.AllocationResult.ResourceAllocation = make(map[string]*pluginapi.ResourceAllocationInfo)
					resp.AllocationResult.ResourceAllocation[curResourceName] = new(pluginapi.ResourceAllocationInfo)
					resp.AllocationResult.ResourceAllocation[curResourceName].Envs = make(map[string]string)
					resp.AllocationResult.ResourceAllocation[curResourceName].Envs[fmt.Sprintf("key%d", i)] = fmt.Sprintf("val%d", i)
					resp.AllocationResult.ResourceAllocation[curResourceName].Annotations = make(map[string]string)
					resp.AllocationResult.ResourceAllocation[curResourceName].Annotations[fmt.Sprintf("key%d", i)] = fmt.Sprintf("val%d", i)
					resp.AllocationResult.ResourceAllocation[curResourceName].IsScalarResource = true
					resp.AllocationResult.ResourceAllocation[curResourceName].IsNodeResource = true
					resp.AllocationResult.ResourceAllocation[curResourceName].AllocatedQuantity = req.ResourceRequests[curResourceName]
					resp.AllocationResult.ResourceAllocation[curResourceName].AllocationResult = "0-1"
					resp.AllocationResult.ResourceAllocation[curResourceName].OciPropertyName = OciPropertyName
					return resp, nil
				},
			})
		} else if res.resourceName == "domain3.com/resource3" {
			manager.registerEndpoint(curResourceName, &pluginapi.ResourcePluginOptions{
				PreStartRequired:      true,
				WithTopologyAlignment: true,
				NeedReconcile:         true,
			}, &MockEndpoint{
				allocateFunc: func(req *pluginapi.ResourceRequest) (*pluginapi.ResourceAllocationResponse, error) {
					return nil, fmt.Errorf("mock error")
				},
			})
		}
	}

	return nil
}

func getTestManager(tmpDir string, activePods ActivePodsFunc, testRes []TestResource) (*ManagerImpl, error) {
	ckm, err := checkpointmanager.NewCheckpointManager(tmpDir)
	if err != nil {
		return nil, err
	}
	testManager := &ManagerImpl{
		socketdir:                        tmpDir,
		socketname:                       "/server.sock",
		allocatedScalarResourcesQuantity: make(map[string]float64),
		endpoints:                        make(map[string]endpointInfo),
		podResources:                     newPodResourcesChk(),
		topologyAffinityStore:            topologymanager.NewFakeManager(),
		activePods:                       activePods,
		sourcesReady:                     &sourcesReadyStub{},
		checkpointManager:                ckm,
		containerRuntime:                 &apitest.FakeRuntimeService{},
		reconcilePeriod:                  5 * time.Second,
	}

	mockStatus := new(statustest.MockStatusProvider)
	testManager.podStatusProvider = mockStatus

	registerEndpointByRes(testManager, testRes)

	return testManager, nil
}
