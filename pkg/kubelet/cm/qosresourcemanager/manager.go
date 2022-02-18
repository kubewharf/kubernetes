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

package qosresourcemanager

import (
	"context"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"k8s.io/klog"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	errorsutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
	"k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager/errors"
	cputopology "k8s.io/kubernetes/pkg/kubelet/cm/cpumanager/topology"
	"k8s.io/kubernetes/pkg/kubelet/cm/qosresourcemanager/checkpoint"
	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager"
	"k8s.io/kubernetes/pkg/kubelet/config"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"k8s.io/kubernetes/pkg/kubelet/metrics"
	"k8s.io/kubernetes/pkg/kubelet/pluginmanager/cache"
	"k8s.io/kubernetes/pkg/kubelet/status"
	schedulernodeinfo "k8s.io/kubernetes/pkg/scheduler/nodeinfo"
	"k8s.io/kubernetes/pkg/util/selinux"
)

// ManagerImpl is the structure in charge of managing Resource Plugins.
type ManagerImpl struct {
	socketname string
	socketdir  string

	endpoints map[string]endpointInfo // Key is ResourceName

	// lock when accesing endpoints and allocatedScalarResourcesQuantity
	mutex sync.Mutex

	server *grpc.Server
	wg     sync.WaitGroup

	// activePods is a method for listing active pods on the node
	// so the amount of pluginResources requested by existing pods
	// could be counted when updating allocated resources
	activePods ActivePodsFunc

	// sourcesReady provides the readiness of kubelet configuration sources such as apiserver update readiness.
	// We use it to determine when we can purge inactive pods from checkpointed state.
	sourcesReady config.SourcesReady

	// contains allocated scalar resources quantity, keyed by resourceName.
	allocatedScalarResourcesQuantity map[string]float64

	// podResources contains pod to allocated resources mapping.
	podResources      *podResourcesChk
	checkpointManager checkpointmanager.CheckpointManager

	// List of NUMA Nodes available on the underlying machine
	numaNodes []int

	// Store of Topology Affinties that the Resource Manager can query.
	topologyAffinityStore topologymanager.Store

	// podStatusProvider provides a method for obtaining pod statuses
	// and the containerID of their containers
	podStatusProvider status.PodStatusProvider

	// containerRuntime is the container runtime service interface needed
	// to make UpdateContainerResources() calls against the containers.
	containerRuntime runtimeService

	// reconcilePeriod is the duration between calls to reconcileState.
	reconcilePeriod time.Duration
}

type endpointInfo struct {
	e    endpoint
	opts *pluginapi.ResourcePluginOptions
}

type sourcesReadyStub struct{}

// PodReusableResources is a map by pod uid of resources to reuse.
type PodReusableResources map[string]ResourceAllocation

func (s *sourcesReadyStub) AddSource(source string) {}
func (s *sourcesReadyStub) AllReady() bool          { return true }

// NewManagerImpl creates a new manager.
func NewManagerImpl(numaNodeInfo cputopology.NUMANodeInfo, topologyAffinityStore topologymanager.Store, reconcilePeriod time.Duration) (Manager, error) {
	return newManagerImpl(pluginapi.KubeletSocket, numaNodeInfo, topologyAffinityStore, reconcilePeriod)
}

func newManagerImpl(socketPath string, numaNodeInfo cputopology.NUMANodeInfo, topologyAffinityStore topologymanager.Store, reconcilePeriod time.Duration) (*ManagerImpl, error) {
	klog.V(2).Infof("[qosresourcemanager] Creating Resource Plugin manager at %s", socketPath)

	if socketPath == "" || !filepath.IsAbs(socketPath) {
		return nil, fmt.Errorf(errBadSocket+" %s", socketPath)
	}

	var numaNodes []int
	for node := range numaNodeInfo {
		numaNodes = append(numaNodes, node)
	}

	dir, file := filepath.Split(socketPath)
	manager := &ManagerImpl{
		endpoints: make(map[string]endpointInfo),

		socketname:                       file,
		socketdir:                        dir,
		numaNodes:                        numaNodes,
		topologyAffinityStore:            topologyAffinityStore,
		podResources:                     newPodResourcesChk(),
		allocatedScalarResourcesQuantity: make(map[string]float64),
		reconcilePeriod:                  reconcilePeriod,
	}

	// The following structures are populated with real implementations in manager.Start()
	// Before that, initializes them to perform no-op operations.
	manager.activePods = func() []*v1.Pod { return []*v1.Pod{} }
	manager.sourcesReady = &sourcesReadyStub{}

	checkpointManager, err := checkpointmanager.NewCheckpointManager(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize checkpoint manager: %v", err)
	}
	manager.checkpointManager = checkpointManager

	return manager, nil
}

func (m *ManagerImpl) removeContents(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range names {
		filePath := filepath.Join(dir, name)
		if filePath == m.checkpointFile() {
			continue
		}
		stat, err := os.Stat(filePath)
		if err != nil {
			klog.Errorf("[qosresourcemanager] Failed to stat file %s: %v", filePath, err)
			continue
		}
		if stat.IsDir() {
			continue
		}
		err = os.RemoveAll(filePath)
		if err != nil {
			errs = append(errs, err)
			klog.Errorf("[qosresourcemanager] Failed to remove file %s: %v", filePath, err)
			continue
		}
	}
	return errorsutil.NewAggregate(errs)
}

// checkpointFile returns resource plugin checkpoint file path.
func (m *ManagerImpl) checkpointFile() string {
	return filepath.Join(m.socketdir, kubeletQoSResourceManagerCheckpoint)
}

// Start starts the QoS Resource Plugin Manager and start initialization of
// podResources and allocatedScalarResourcesQuantity information from checkpointed state and
// starts resource plugin registration service.
func (m *ManagerImpl) Start(activePods ActivePodsFunc, sourcesReady config.SourcesReady, podStatusProvider status.PodStatusProvider, containerRuntime runtimeService) error {
	klog.V(2).Infof("[qosresourcemanager] Starting Resource Plugin manager")

	m.activePods = activePods
	m.sourcesReady = sourcesReady
	m.podStatusProvider = podStatusProvider
	m.containerRuntime = containerRuntime

	// Loads in podResources information from disk.
	err := m.readCheckpoint()
	if err != nil {
		klog.Warningf("[qosresourcemanager] Continue after failing to read checkpoint file. Resource allocation info may NOT be up-to-date. Err: %v", err)
	}

	socketPath := filepath.Join(m.socketdir, m.socketname)
	if err = os.MkdirAll(m.socketdir, 0750); err != nil {
		return err
	}
	if selinux.SELinuxEnabled() {
		if err := selinux.SetFileLabel(m.socketdir, config.KubeletPluginsDirSELinuxLabel); err != nil {
			klog.Warningf("[qosresourcemanager] Unprivileged containerized plugins might not work. Could not set selinux context on %s: %v", m.socketdir, err)
		}
	}

	// Removes all stale sockets in m.socketdir. Resource plugins can monitor
	// this and use it as a signal to re-register with the new Kubelet.
	if err := m.removeContents(m.socketdir); err != nil {
		klog.Errorf("[qosresourcemanager] Fail to clean up stale contents under %s: %v", m.socketdir, err)
	}

	s, err := net.Listen("unix", socketPath)
	if err != nil {
		klog.Errorf(errListenSocket+" %v", err)
		return err
	}

	m.wg.Add(1)
	m.server = grpc.NewServer([]grpc.ServerOption{}...)

	pluginapi.RegisterRegistrationServer(m.server, m)

	ctx, cancel := context.WithCancel(context.Background())

	klog.V(2).Infof("[qosresourcemanager] Serving resource plugin registration server on %q", socketPath)
	go func() {
		defer func() {
			m.wg.Done()
			cancel()
		}()
		m.server.Serve(s)
	}()

	klog.Infof("[qosresourcemanager] reconciling every %v", m.reconcilePeriod)

	// Periodically call m.reconcileState() to continue to keep the resources allocation for
	// all active pods in sync with the latest result allocated by corresponding resource plugin
	go wait.Until(func() { m.reconcileState() }, m.reconcilePeriod, ctx.Done())

	return nil
}

// GetWatcherHandler returns the plugin handler
func (m *ManagerImpl) GetWatcherHandler() cache.PluginHandler {
	if f, err := os.Create(m.socketdir + "DEPRECATION"); err != nil {
		klog.Errorf("Failed to create deprecation file at %s", m.socketdir)
	} else {
		f.Close()
		klog.V(4).Infof("created deprecation file %s", f.Name())
	}

	return cache.PluginHandler(m)
}

// ValidatePlugin validates a plugin if the version is correct and the name has the format of an extended resource
func (m *ManagerImpl) ValidatePlugin(pluginName string, endpoint string, versions []string) error {
	klog.V(2).Infof("Got Plugin %s at endpoint %s with versions %v", pluginName, endpoint, versions)

	if !m.isVersionCompatibleWithPlugin(versions) {
		return fmt.Errorf("manager version, %s, is not among plugin supported versions %v", pluginapi.Version, versions)
	}

	return nil
}

// RegisterPlugin starts the endpoint and registers it
func (m *ManagerImpl) RegisterPlugin(pluginName string, endpoint string, versions []string) error {
	klog.V(2).Infof("[qosresourcemanager] Registering Plugin %s at endpoint %s", pluginName, endpoint)

	e, err := newEndpointImpl(endpoint, pluginName)
	if err != nil {
		return fmt.Errorf("[qosresourcemanager] failed to dial resource plugin with socketPath %s: %v", endpoint, err)
	}

	options, err := e.client.GetResourcePluginOptions(context.Background(), &pluginapi.Empty{})
	if err != nil {
		return fmt.Errorf("[qosresourcemanager] failed to get resource plugin options: %v", err)
	}

	m.registerEndpoint(pluginName, options, e)

	return nil
}

// DeRegisterPlugin deregisters the plugin
// TODO work on the behavior for deregistering plugins
// e.g: Should we delete the resource
func (m *ManagerImpl) DeRegisterPlugin(pluginName string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if eI, ok := m.endpoints[pluginName]; ok {
		eI.e.stop()
	}
}

func (m *ManagerImpl) isVersionCompatibleWithPlugin(versions []string) bool {
	// TODO(sunjianyu): Currently this is fine as we only have a single supported version. When we do need to support
	// multiple versions in the future, we may need to extend this function to return a supported version.
	// E.g., say kubelet supports v1beta1 and v1beta2, and we get v1alpha1 and v1beta1 from a resource plugin,
	// this function should return v1beta1
	for _, version := range versions {
		for _, supportedVersion := range pluginapi.SupportedVersions {
			if version == supportedVersion {
				return true
			}
		}
	}
	return false
}

// Allocate is the call that you can use to allocate a set of resources
// from the registered resource plugins.
func (m *ManagerImpl) Allocate(pod *v1.Pod, container *v1.Container) error {
	if pod == nil || container == nil {
		return fmt.Errorf("Allocate got nil pod: %v or container: %v", pod, container)
	}

	if isSkippedPod(pod) {
		klog.V(4).Infof("[qosresourcemanager] skip pod resource allocation")
		return nil
	}

	containerType, containerIndex, err := GetContainerTypeAndIndex(pod, container)

	if err != nil {
		return fmt.Errorf("GetContainerTypeAndIndex failed with error: %v", err)
	}

	if err = m.allocateContainerResources(pod, container, containerType, containerIndex); err != nil {
		return err
	}
	return nil
}

// allocateContainerResources attempts to allocate all of required resource
// plugin resources for the input container, issues an Allocate rpc request
// for each new resource resource requirement, processes their AllocateResponses,
// and updates the cached containerResources on success.
func (m *ManagerImpl) allocateContainerResources(pod *v1.Pod, container *v1.Container, containerType pluginapi.ContainerType, containerIndex uint64) error {
	if pod == nil || container == nil {
		return fmt.Errorf("allocateContainerResources met nil pod: %v or container: %v", pod, container)
	}

	podUID := string(pod.UID)
	contName := container.Name
	allocatedResourcesUpdated := false
	// [TODO](sunjianyu): for accompanying resources, we may support request those resources in annotation later
	for k, v := range container.Resources.Requests {
		resource := string(k)
		needed := int(v.Value())

		if !m.isResourcePluginResource(resource) {
			klog.Infof("[qosresourcemanager] resource %s is not supported by any resource plugin", resource)
			continue
		}

		klog.Infof("[qosresourcemanager] pod: %s/%s container: %s needs %d %s",
			pod.Namespace, pod.Name, container.Name, needed, resource)

		// Updates allocated resources to garbage collect any stranded resources
		// before doing the resource plugin allocation.
		if !allocatedResourcesUpdated {
			m.UpdateAllocatedResources()
			allocatedResourcesUpdated = true
		}

		// short circuit to regenerate the same allocationInfo if there are already
		// allocated to the Container. This might happen after a
		// kubelet restart, for example.
		// think about a parent resource name with accompanying resources, we only check the result of the parent resource.
		// if you want to allocate for accompanying resources every times, you can set the parent resource as non-scalar resource or set allocated quantity as zero
		allocationInfo := m.podResources.containerResource(string(pod.UID), container.Name, resource)
		if allocationInfo != nil {

			allocated := int(math.Ceil(allocationInfo.AllocatedQuantity))

			if allocationInfo.IsScalarResource && allocated >= needed {
				klog.Infof("[qosresourcemanager] resource %s already allocated to (pod %s/%s, container %v) with larger number than request: requested: %d, allocated: %d; not to allocate",
					resource, pod.GetNamespace(), pod.GetName(), container.Name, needed, allocated)
				continue
			} else {
				klog.Warningf("[qosresourcemanager] resource %s already allocated to (pod %s/%s, container %v) with smaller number than request: requested: %d, allocated: %d; continue to allocate",
					resource, pod.GetNamespace(), pod.GetName(), container.Name, needed, allocated)
			}
		}

		startRPCTime := time.Now()
		// Manager.Allocate involves RPC calls to resource plugin, which
		// could be heavy-weight. Therefore we want to perform this operation outside
		// mutex lock. Note if Allocate call fails, we may leave container resources
		// partially allocated for the failed container. We rely on UpdateAllocatedResources()
		// to garbage collect these resources later. Another side effect is that if
		// we have X resource A and Y resource B in total, and two containers, container1
		// and container2 both require X resource A and Y resource B. Both allocation
		// requests may fail if we serve them in mixed order.
		// TODO: may revisit this part later if we see inefficient resource allocation
		// in real use as the result of this. Should also consider to parallelize resource
		// plugin Allocate grpc calls if it becomes common that a container may require
		// resources from multiple resource plugins.
		m.mutex.Lock()
		eI, ok := m.endpoints[resource]
		m.mutex.Unlock()
		if !ok {
			return fmt.Errorf("unknown Resource Plugin %s", resource)
		}

		// TODO: refactor this part of code to just append a ContainerAllocationRequest
		// in a passed in AllocateRequest pointer, and issues a single Allocate call per pod.
		klog.V(3).Infof("[qosresourcemanager] making allocation request of %.3f resources %s for pod: %s/%s; container: %s",
			ParseQuantityToFloat64(v), resource, pod.Namespace, pod.Name, container.Name)

		resourceReq := &pluginapi.ResourceRequest{
			PodUid:           string(pod.GetUID()),
			PodNamespace:     pod.GetNamespace(),
			PodName:          pod.GetName(),
			ContainerName:    container.Name,
			ContainerType:    containerType,
			ContainerIndex:   containerIndex,
			PodRole:          pod.Labels[pluginapi.PodRoleLabelKey],
			PodType:          pod.Annotations[pluginapi.PodTypeAnnotationKey],
			ResourceName:     resource,
			ResourceRequests: map[string]float64{resource: ParseQuantityToFloat64(v)},
		}

		if m.resourceHasTopologyAlignment(resource) {
			hint := m.topologyAffinityStore.GetAffinity(podUID, contName)

			if hint.NUMANodeAffinity == nil {
				klog.Warningf("[qosresourcemanager] pod: %s/%s; container: %s allocate resouce: %s without numa nodes affinity",
					pod.Namespace, pod.Name, container.Name, resource)
			} else {
				klog.Warningf("[qosresourcemanager] pod: %s/%s; container: %s allocate resouce: %s get hint: %v from store",
					pod.Namespace, pod.Name, container.Name, resource, hint)
			}

			resourceReq.Hint = ParseTopologyManagerHint(hint)
		}

		ctx, err := GetContextWithSpecificInfo(pod, container)

		if err != nil {
			return fmt.Errorf("GetContextWithSpecificInfo failed with error: %v", err)
		}

		resp, err := eI.e.allocate(ctx, resourceReq)
		metrics.ResourcePluginAllocationDuration.WithLabelValues(resource).Observe(metrics.SinceInSeconds(startRPCTime))
		if err != nil {
			return err
		}

		// Update internal cached podResources state.
		if resp.AllocatationResult == nil {
			klog.Warningf("[qosresourcemanager] allocation request for resources %s for pod: %s/%s, container: %s got nil allocation result",
				resource, pod.Namespace, pod.Name, container.Name)
			continue
		}

		// [TODO](sunjianyu): to think abount a method to aviod accompanying resouce names conflict
		for accResourceName, allocationInfo := range resp.AllocatationResult.ResourceAllocation {
			if allocationInfo == nil {
				klog.Warningf("[qosresourcemanager] allocation request for resources %s - accompanying resource: %s for pod: %s/%s, container: %s got nil allocation infomation",
					resource, accResourceName, pod.Namespace, pod.Name, container.Name)
				continue
			}

			klog.V(4).Infof("[qosresourcemanager] allocation information for resources %s - accompanying resource: %s for pod: %s/%s, container: %s is %v",
				resource, accResourceName, pod.Namespace, pod.Name, container.Name, *allocationInfo)

			m.podResources.insert(podUID, contName, accResourceName, allocationInfo)

			allocatedScalarResourcesQuantity := m.podResources.scalarResourcesQuantity()

			m.mutex.Lock()
			m.allocatedScalarResourcesQuantity = allocatedScalarResourcesQuantity
			m.mutex.Unlock()
		}
	}

	// Checkpoints resource to container allocation information.
	return m.writeCheckpoint()
}

// UpdatePluginResources updates node resources based on resources already allocated to pods.
func (m *ManagerImpl) UpdatePluginResources(node *schedulernodeinfo.NodeInfo, attrs *lifecycle.PodAdmitAttributes) error {
	pod := attrs.Pod

	// quick return if no pluginResources requested
	if m.podResources.podResources(string(pod.UID)) == nil {
		return nil
	}

	m.sanitizeNodeAllocatable(node)
	return nil
}

// Register registers a resource plugin.
func (m *ManagerImpl) Register(ctx context.Context, r *pluginapi.RegisterRequest) (*pluginapi.Empty, error) {
	klog.Infof("[qosresourcemanager] Got registration request from resource plugin with resource name %q", r.ResourceName)
	metrics.ResourcePluginRegistrationCount.WithLabelValues(r.ResourceName).Inc()
	var versionCompatible bool
	for _, v := range pluginapi.SupportedVersions {
		if r.Version == v {
			versionCompatible = true
			break
		}
	}
	if !versionCompatible {
		errorString := fmt.Sprintf(errUnsupportedVersion, r.Version, pluginapi.SupportedVersions)
		klog.Infof("Bad registration request from resource plugin with resource name %q: %s", r.ResourceName, errorString)
		return &pluginapi.Empty{}, fmt.Errorf(errorString)
	}

	// TODO: for now, always accepts newest resource plugin. Later may consider to
	// add some policies here, e.g., verify whether an old resource plugin with the
	// same resource name is still alive to determine whether we want to accept
	// the new registration.
	success := make(chan bool)
	go m.addEndpoint(r, success)
	select {
	case pass := <-success:
		if pass {
			klog.Infof("[qosresourcemanager] Register resource plugin for %s success", r.ResourceName)
			return &pluginapi.Empty{}, nil
		}
		klog.Errorf("[qosresourcemanager] Register resource plugin for %s fail", r.ResourceName)
		return &pluginapi.Empty{}, fmt.Errorf("failed to register resource %s", r.ResourceName)
	case <-ctx.Done():
		klog.Errorf("[qosresourcemanager] Register resource plugin for %s timeout", r.ResourceName)
		return &pluginapi.Empty{}, fmt.Errorf("timeout to register resource %s", r.ResourceName)
	}
}

// Stop is the function that can stop the gRPC server.
// Can be called concurrently, more than once, and is safe to call
// without a prior Start.
func (m *ManagerImpl) Stop() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	for _, eI := range m.endpoints {
		eI.e.stop()
	}

	if m.server == nil {
		return nil
	}
	m.server.Stop()
	m.wg.Wait()
	m.server = nil
	return nil
}

func (m *ManagerImpl) registerEndpoint(resourceName string, options *pluginapi.ResourcePluginOptions, e endpoint) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	old, ok := m.endpoints[resourceName]

	if ok && !old.e.isStopped() {
		klog.V(2).Infof("[qosresourcemanager] stop old endpoint: %v", old.e)
		old.e.stop()
	}

	m.endpoints[resourceName] = endpointInfo{e: e, opts: options}
	klog.V(2).Infof("[qosresourcemanager] Registered endpoint %v", e)
}

func (m *ManagerImpl) addEndpoint(r *pluginapi.RegisterRequest, success chan<- bool) {
	new, err := newEndpointImpl(filepath.Join(m.socketdir, r.Endpoint), r.ResourceName)
	if err != nil {
		klog.Errorf("[qosresourcemanager] Failed to dial resource plugin with request %v: %v", r, err)
		success <- false
		return
	}
	m.registerEndpoint(r.ResourceName, r.Options, new)
	success <- true
}

func (m *ManagerImpl) GetTopologyAwareResources(podUID, containerName string) (*pluginapi.GetTopologyAwareResourcesResponse, error) {
	var resp *pluginapi.GetTopologyAwareResourcesResponse

	m.mutex.Lock()
	for resourceName, eI := range m.endpoints {
		if eI.e.isStopped() {
			klog.Warningf("[qosresourcemanager] skip GetTopologyAwareResources of resource: %s for pod: %s container: %s, because plugin stopped",
				resourceName, podUID, containerName)
			continue
		}

		ctx := metadata.NewOutgoingContext(context.Background(), metadata.New(nil))
		m.mutex.Unlock()
		curResp, err := eI.e.getTopologyAwareResources(ctx, &pluginapi.GetTopologyAwareResourcesRequest{
			PodUid:        podUID,
			ContainerName: containerName,
		})
		m.mutex.Lock()

		if err != nil {
			m.mutex.Unlock()
			//[TODO](sunjianyu): to discuss if we should return err if only one resource plugin gets error?
			return nil, fmt.Errorf("getTopologyAwareResources for resource: %s failed with error: %v", resourceName, err)
		} else if curResp == nil {
			klog.Warningf("[qosresourcemanager] getTopologyAwareResources of resource: %s for pod: %s container: %s, got nil response but without error",
				resourceName, podUID, containerName)
			continue
		}

		if resp == nil {
			resp = curResp

			if resp.ContainerTopologyAwareResources == nil {
				resp.ContainerTopologyAwareResources = &pluginapi.ContainerTopologyAwareResources{
					ContainerName: containerName,
				}
			}

			if resp.ContainerTopologyAwareResources.AllocatedResources == nil {
				resp.ContainerTopologyAwareResources.AllocatedResources = &pluginapi.TopologyAwareResources{}
			}

			if resp.ContainerTopologyAwareResources.AllocatedResources.TopologyAwareResources == nil {
				resp.ContainerTopologyAwareResources.AllocatedResources.TopologyAwareResources = make(map[string]*pluginapi.TopologyAwareResource)
			}
		} else if curResp.ContainerTopologyAwareResources != nil && curResp.ContainerTopologyAwareResources.AllocatedResources != nil {
			for resourceName, topologyAwareResource := range curResp.ContainerTopologyAwareResources.AllocatedResources.TopologyAwareResources {
				if topologyAwareResource != nil {
					resp.ContainerTopologyAwareResources.AllocatedResources.TopologyAwareResources[resourceName] = proto.Clone(topologyAwareResource).(*pluginapi.TopologyAwareResource)
				}
			}
		} else {
			klog.Warningf("[qosresourcemanager] getTopologyAwareResources of resource: %s for pod: %s container: %s, get nil resp or nil topologyAwareResources in resp",
				resourceName, podUID, containerName)
		}
	}
	m.mutex.Unlock()
	return resp, nil
}

func (m *ManagerImpl) GetTopologyAwareAllocatableResources() (*pluginapi.GetTopologyAwareAllocatableResourcesResponse, error) {
	var resp *pluginapi.GetTopologyAwareAllocatableResourcesResponse

	m.mutex.Lock()
	for resourceName, eI := range m.endpoints {
		if eI.e.isStopped() {
			klog.Warningf("[qosresourcemanager] skip GetTopologyAwareAllocatableResources of resource: %s, because plugin stopped", resourceName)
			continue
		}
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.New(nil))
		m.mutex.Unlock()
		curResp, err := eI.e.getTopologyAwareAllocatableResources(ctx, &pluginapi.GetTopologyAwareAllocatableResourcesRequest{})
		m.mutex.Lock()

		if err != nil {
			m.mutex.Unlock()
			//[TODO](sunjianyu): to discuss if we should return err if only one resource plugin gets error?
			return nil, fmt.Errorf("getTopologyAwareAllocatableResources for resource: %s failed with error: %v", resourceName, err)
		} else if curResp == nil {
			klog.Warningln("[qosresourcemanager] getTopologyAwareAllocatableResources got nil response but without error")
			continue
		}

		if resp == nil {
			resp = curResp

			if resp.AllocatableResources == nil {
				resp.AllocatableResources = &pluginapi.TopologyAwareResources{}
			}

			if resp.AllocatableResources.TopologyAwareResources == nil {
				resp.AllocatableResources.TopologyAwareResources = make(map[string]*pluginapi.TopologyAwareResource)
			}

		} else if curResp.AllocatableResources != nil {
			for resourceName, topologyAwareResource := range curResp.AllocatableResources.TopologyAwareResources {
				if topologyAwareResource != nil {
					resp.AllocatableResources.TopologyAwareResources[resourceName] = proto.Clone(topologyAwareResource).(*pluginapi.TopologyAwareResource)
				}
			}
		} else {
			klog.Warningf("[qosresourcemanager] getTopologyAwareAllocatableResources of resource: %s, get nil resp or nil topologyAwareResources in resp", resourceName)
		}
	}
	m.mutex.Unlock()
	return resp, nil
}

// GetCapacity is expected to be called when Kubelet updates its node status.
// The first returned variable contains the registered resource plugin resource capacity.
// The second returned variable contains the registered resource plugin resource allocatable.
// The third returned variable contains previously registered resources that are no longer active.
// Kubelet uses this information to update resource capacity/allocatable in its node status.
// After the call, resource plugin can remove the inactive resources from its internal list as the
// change is already reflected in Kubelet node status.
// Note in the special case after Kubelet restarts, resource plugin resource capacities can
// temporarily drop to zero till corresponding resource plugins re-register. This is OK because
// cm.UpdatePluginResource() run during predicate Admit guarantees we adjust nodeinfo
// capacity for already allocated pods so that they can continue to run. However, new pods
// requiring resource plugin resources will not be scheduled till resource plugin re-registers.
func (m *ManagerImpl) GetCapacity() (v1.ResourceList, v1.ResourceList, []string) {
	var capacity = v1.ResourceList{}
	var allocatable = v1.ResourceList{}
	deletedResources := sets.NewString()
	m.mutex.Lock()
	// [TODO](sunjianyu): consider we need diff capacity and allocatable here?
	for resourceName, eI := range m.endpoints {
		if eI.e.stopGracePeriodExpired() {
			delete(m.endpoints, resourceName)
			deletedResources.Insert(resourceName)
		} else {
			ctx := metadata.NewOutgoingContext(context.Background(), metadata.New(nil))
			m.mutex.Unlock()
			resp, err := eI.e.getTopologyAwareAllocatableResources(ctx, &pluginapi.GetTopologyAwareAllocatableResourcesRequest{})
			m.mutex.Lock()
			if err != nil {
				klog.Errorf("[qosresourcemanager] getTopologyAwareAllocatableResources for resource: %s failed with error: %v", resourceName, err)
				// [TODO](sunjianyu): consider if it will cause resource quantity vibrating?
				capacity[v1.ResourceName(resourceName)] = *resource.NewQuantity(0, resource.DecimalSI)
				allocatable[v1.ResourceName(resourceName)] = *resource.NewQuantity(0, resource.DecimalSI)
			} else if resp == nil ||
				resp.AllocatableResources == nil ||
				len(resp.AllocatableResources.TopologyAwareResources) == 0 {
				klog.Warningf("[qosresourcemanager] getTopologyAwareAllocatableResources for resource: %s got nil response or empty content in response", resourceName)
				capacity[v1.ResourceName(resourceName)] = *resource.NewQuantity(0, resource.DecimalSI)
				allocatable[v1.ResourceName(resourceName)] = *resource.NewQuantity(0, resource.DecimalSI)
			} else {
				for accResourceName, taResource := range resp.AllocatableResources.TopologyAwareResources {

					if taResource == nil {
						klog.Errorf("[qosresourcemanager] accResourceName: %s with nil topology aware resource", accResourceName)
						capacity[v1.ResourceName(accResourceName)] = *resource.NewQuantity(0, resource.DecimalSI)
						allocatable[v1.ResourceName(accResourceName)] = *resource.NewQuantity(0, resource.DecimalSI)
						continue
					}

					if taResource.IsNodeResource && taResource.IsScalarResource {
						aggregatedQuantity, _ := resource.ParseQuantity(fmt.Sprintf("%.3f", taResource.AggregatedQuantity))
						capacity[v1.ResourceName(accResourceName)] = aggregatedQuantity
						allocatable[v1.ResourceName(accResourceName)] = aggregatedQuantity
					}
				}
			}
		}
	}
	m.mutex.Unlock()
	return capacity, allocatable, deletedResources.UnsortedList()
}

// Checkpoints resource to container allocation information to disk.
func (m *ManagerImpl) writeCheckpoint() error {
	data := checkpoint.New(m.podResources.toCheckpointData())
	err := m.checkpointManager.CreateCheckpoint(kubeletQoSResourceManagerCheckpoint, data)
	if err != nil {
		err2 := fmt.Errorf("[qosresourcemanager] failed to write checkpoint file %q: %v", kubeletQoSResourceManagerCheckpoint, err)
		klog.Warning(err2)
		return err2
	}
	return nil
}

// Reads resource to container allocation information from disk, and populates
// m.allocatedScalarResourcesQuantity accordingly.
func (m *ManagerImpl) readCheckpoint() error {
	resEntries := make([]checkpoint.PodResourcesEntry, 0)
	cp := checkpoint.New(resEntries)
	err := m.checkpointManager.GetCheckpoint(kubeletQoSResourceManagerCheckpoint, cp)
	if err != nil {
		if err == errors.ErrCheckpointNotFound {
			klog.Warningf("[qosresourcemanager] Failed to retrieve checkpoint for %q: %v", kubeletQoSResourceManagerCheckpoint, err)
			return nil
		}
		return err
	}

	podResources := cp.GetData()
	m.podResources.fromCheckpointData(podResources)
	allocatedScalarResourcesQuantity := m.podResources.scalarResourcesQuantity()

	m.mutex.Lock()
	m.allocatedScalarResourcesQuantity = allocatedScalarResourcesQuantity

	allocatedResourceNames := m.podResources.allAllocatedResourceNames()

	for _, allocatedResourceName := range allocatedResourceNames.UnsortedList() {
		m.endpoints[allocatedResourceName] = endpointInfo{e: newStoppedEndpointImpl(allocatedResourceName), opts: nil}
	}

	m.mutex.Unlock()

	return nil
}

// UpdateAllocatedResources frees any Resources that are bound to terminated pods.
func (m *ManagerImpl) UpdateAllocatedResources() {
	activePods := m.activePods()
	if !m.sourcesReady.AllReady() {
		return
	}
	podsToBeRemoved := m.podResources.pods()
	for _, pod := range activePods {
		podsToBeRemoved.Delete(string(pod.UID))
	}
	if len(podsToBeRemoved) <= 0 {
		return
	}

	podsToBeRemovedList := podsToBeRemoved.UnsortedList()
	klog.V(3).Infof("[qosresourcemanager] pods to be removed: %v", podsToBeRemovedList)
	m.podResources.delete(podsToBeRemovedList)

	// Regenerated allocatedScalarResourcesQuantity after we update pod allocation information.
	allocatedScalarResourcesQuantity := m.podResources.scalarResourcesQuantity()
	m.mutex.Lock()
	m.allocatedScalarResourcesQuantity = allocatedScalarResourcesQuantity
	m.mutex.Unlock()

	err := m.writeCheckpoint()

	if err != nil {
		klog.Errorf("[qosresourcemanager.UpdateAllocatedResources] write checkpoint failed with error: %v", err)
	}

	m.mutex.Lock()
	for resourceName, eI := range m.endpoints {
		if eI.e.isStopped() {
			klog.Warningf("[qosresourcemanager] skip removePods: %+v of resource: %s, because plugin stopped", podsToBeRemovedList, resourceName)
			continue
		}

		for _, podUID := range podsToBeRemovedList {
			ctx := metadata.NewOutgoingContext(context.Background(), metadata.New(nil))
			m.mutex.Unlock()
			_, err := eI.e.removePod(ctx, &pluginapi.RemovePodRequest{
				PodUid: podUID,
			})
			m.mutex.Lock()

			if err != nil {
				klog.Errorf("[qosresourcemanager.UpdateAllocatedResources] remove pod: %s in %s endpoint failed with error: %v", podUID, resourceName, err)
			}
		}
	}
	m.mutex.Unlock()
}

// GetResourceRunContainerOptions checks whether we have cached containerResources
// for the passed-in <pod, container> and returns its ResourceRunContainerOptions
// for the found one. An empty struct is returned in case no cached state is found.
func (m *ManagerImpl) GetResourceRunContainerOptions(pod *v1.Pod, container *v1.Container) (*kubecontainer.ResourceRunContainerOptions, error) {
	if isSkippedPod(pod) {
		klog.V(4).Infof("[qosresourcemanager] skip get resource run container options")
		return nil, nil
	}

	podUID := string(pod.UID)
	contName := container.Name

	resources := m.podResources.containerAllResources(podUID, contName)

	// [TODO](sunjianyu): for accompanying resources, we may support request those resources in annotation later
	// think about a parent resource name with accompanying resources,
	// we must return the result of the parent resource to aviod reallocating.
	needsReAllocate := false
	for k, v := range container.Resources.Requests {
		resourceName := string(k)
		if !m.isResourcePluginResource(resourceName) {
			continue
		}
		if v.Value() == 0 {
			continue
		}
		err := m.callPreStartContainerIfNeeded(pod, container, resourceName)
		if err != nil {
			return nil, err
		}

		// This is a resource plugin resource yet we don't have cached
		// resource state. This is likely due to a race during node
		// restart. We re-issue allocate request to cover this race.
		if resources[resourceName] == nil && !isSkippedContainer(pod, container) {
			needsReAllocate = true
		}
	}
	if needsReAllocate {
		klog.V(2).Infof("[qosresourcemanager] needs re-allocate resource plugin resources for pod %s, container %s", podUID, container.Name)
		if err := m.Allocate(pod, container); err != nil {
			return nil, err
		}
	}

	return m.podResources.resourceRunContainerOptions(string(pod.UID), container.Name)
}

// callPreStartContainerIfNeeded issues PreStartContainer grpc call for resource plugin resource
// with PreStartRequired option set.
func (m *ManagerImpl) callPreStartContainerIfNeeded(pod *v1.Pod, container *v1.Container, resource string) error {
	m.mutex.Lock()
	eI, ok := m.endpoints[resource]
	if !ok {
		m.mutex.Unlock()
		return fmt.Errorf("endpoint not found in cache for a registered resource: %s", resource)
	}

	if eI.opts == nil || !eI.opts.PreStartRequired {
		m.mutex.Unlock()
		klog.V(4).Infof("[qosresourcemanager] resource plugin options indicate to skip PreStartContainer for resource: %s", resource)
		return nil
	}

	m.mutex.Unlock()
	klog.V(4).Infof("[qosresourcemanager] Issuing an PreStartContainer call for container, %s, of pod %s", container.Name, pod.Name)
	_, err := eI.e.preStartContainer(pod, container)
	if err != nil {
		return fmt.Errorf("resource plugin PreStartContainer rpc failed with err: %v", err)
	}
	// TODO: Add metrics support for init RPC
	return nil
}

// sanitizeNodeAllocatable scans through allocatedScalarResourcesQuantity in the qos resource manager
// and if necessary, updates allocatableResource in nodeInfo to at least equal to
// the allocated capacity. This allows pods that have already been scheduled on
// the node to pass GeneralPredicates admission checking even upon resource plugin failure.
func (m *ManagerImpl) sanitizeNodeAllocatable(node *schedulernodeinfo.NodeInfo) {
	var newAllocatableResource *schedulernodeinfo.Resource
	allocatableResource := node.AllocatableResource()
	if allocatableResource.ScalarResources == nil {
		allocatableResource.ScalarResources = make(map[v1.ResourceName]int64)
	}
	m.mutex.Lock()
	for resource, allocatedQuantity := range m.allocatedScalarResourcesQuantity {
		quant, ok := allocatableResource.ScalarResources[v1.ResourceName(resource)]
		if ok && float64(quant) >= allocatedQuantity {
			continue
		}
		// Needs to update nodeInfo.AllocatableResource to make sure
		// NodeInfo.allocatableResource at least equal to the capacity already allocated.
		if newAllocatableResource == nil {
			newAllocatableResource = allocatableResource.Clone()
		}
		newAllocatableResource.ScalarResources[v1.ResourceName(resource)] = int64(math.Ceil(allocatedQuantity))
	}
	m.mutex.Unlock()

	if newAllocatableResource != nil {
		node.SetAllocatableResource(newAllocatableResource)
	}
}

func (m *ManagerImpl) isResourcePluginResource(resource string) bool {
	m.mutex.Lock()
	_, registeredResource := m.endpoints[resource]
	m.mutex.Unlock()

	if registeredResource {
		return true
	}

	allocatedResourceNames := m.podResources.allAllocatedResourceNames()
	return allocatedResourceNames.Has(resource)
}

// ShouldResetExtendedResourceCapacity returns whether the extended resources should be zeroed or not,
// depending on whether the node has been recreated. Absence of the checkpoint file strongly indicates the node
// has been recreated.
func (m *ManagerImpl) ShouldResetExtendedResourceCapacity() bool {
	if utilfeature.DefaultFeatureGate.Enabled(features.QoSResourceManager) {
		checkpoints, err := m.checkpointManager.ListCheckpoints()
		if err != nil {
			return false
		}
		return len(checkpoints) == 0
	}
	return false
}

func (m *ManagerImpl) reconcileState() {
	klog.Infof("[qosresourcemanager.reconcileState] reconciling")

	m.UpdateAllocatedResources()

	activePods := m.activePods()

	resourceAllocationResps := make([]*pluginapi.GetResourcesAllocationResponse, 0, len(m.endpoints))

	m.mutex.Lock()
	for resourceName, eI := range m.endpoints {
		if eI.e.isStopped() {
			klog.Warningf("[qosresourcemanager.reconcileState] skip getResourceAllocation of resource: %s, because plugin stopped", resourceName)
			continue
		} else if !eI.opts.NeedReconcile {
			klog.V(6).Infof("[qosresourcemanager.reconcileState] skip getResourceAllocation of resource: %s, because plugin needn't reconciling", resourceName)
			continue
		}

		ctx := metadata.NewOutgoingContext(context.Background(), metadata.New(nil))
		m.mutex.Unlock()
		resp, err := eI.e.getResourceAllocation(ctx, &pluginapi.GetResourcesAllocationRequest{})
		m.mutex.Lock()

		if err != nil {
			klog.Errorf("[qosresourcemanager.reconcileState] getResourceAllocation to %s endpoint failed with error: %v", resourceName, err)
			continue
		}

		resourceAllocationResps = append(resourceAllocationResps, resp)
	}
	m.mutex.Unlock()

	for _, pod := range activePods {
		if isSkippedPod(pod) {
			klog.V(4).Infof("[qosresourcemanager] skip active pod reconcile")
			continue
		}

		nContainers := len(pod.Spec.Containers)
		for i := 0; i < nContainers; i++ {
			podUID := string(pod.UID)
			containerName := pod.Spec.Containers[i].Name

			pstatus, ok := m.podStatusProvider.GetPodStatus(pod.UID)
			if !ok {
				klog.Warningf("[qosresourcemanager.reconcileState] reconcileState: skipping pod; status not found (pod: %s/%s, container: %s)", pod.Namespace, pod.Name, containerName)
				continue
			}

			containerID, err := findContainerIDByName(&pstatus, containerName)
			if err != nil {
				klog.Warningf("[qosresourcemanager.reconcileState] reconcileState: skipping container; ID not found in pod status (pod: %s/%s, container: %s, error: %v)",
					pod.Namespace, pod.Name, containerName, err)
				continue
			}

			for _, resp := range resourceAllocationResps {
				if resp != nil && resp.PodResources[podUID] != nil && resp.PodResources[podUID].ContainerResources[containerName] != nil {
					resourceAllocations := resp.PodResources[podUID].ContainerResources[containerName]
					for resourceName, resourceAllocationInfo := range resourceAllocations.ResourceAllocation {
						m.podResources.insert(podUID, containerName, resourceName, resourceAllocationInfo)
					}
				}
			}

			err = m.updateContainerResources(podUID, containerName, containerID)

			if err != nil {
				klog.Errorf("[qosresourcemanager.reconcileState] pod: %s/%s, container: %s, updateContainerResources failed with error: %v",
					pod.Namespace, pod.Name, containerName, err)
				continue
			} else {
				klog.Infof("[qosresourcemanager.reconcileState] pod: %s/%s, container: %s, reconcile state successfully",
					pod.Namespace, pod.Name, containerName)
			}
		}
	}
}

func (m *ManagerImpl) updateContainerResources(podUID, containerName, containerID string) error {
	opts, err := m.podResources.resourceRunContainerOptions(podUID, containerName)

	if err != nil {
		return fmt.Errorf("updateContainerResources failed with error: %v", err)
	} else if opts == nil {
		// [TODO]: should we judge reallocation here to aviod entry of the container in podResources is missing
		klog.Warningf("[qosresourcemanager.updateContainerResources] there is no resources opts for pod: %s, container: %s",
			podUID, containerName)
		return nil
	}

	return m.containerRuntime.UpdateContainerResources(
		containerID,
		opts.Resources)
}
