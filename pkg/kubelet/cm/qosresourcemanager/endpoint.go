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
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	pluginapi "k8s.io/kubelet/pkg/apis/resourceplugin/v1alpha1"
)

// endpoint maps to a single registered resource plugin. It is responsible
// for managing gRPC communications with the resource plugin and caching
// resource states reported by the resource plugin.
type endpoint interface {
	// [TODO] if we need list&watch resource plugin,
	// then we need run function.
	//run(success chan<- bool)
	stop()
	allocate(c context.Context, resourceRequest *pluginapi.ResourceRequest) (*pluginapi.ResourceAllocationResponse, error)
	getTopologyHints(c context.Context, resourceRequest *pluginapi.ResourceRequest) (*pluginapi.ResourceHintsResponse, error)
	getTopologyAwareAllocatableResources(c context.Context, request *pluginapi.GetTopologyAwareAllocatableResourcesRequest) (*pluginapi.GetTopologyAwareAllocatableResourcesResponse, error)
	getTopologyAwareResources(c context.Context, request *pluginapi.GetTopologyAwareResourcesRequest) (*pluginapi.GetTopologyAwareResourcesResponse, error)
	preStartContainer(pod *v1.Pod, container *v1.Container) (*pluginapi.PreStartContainerResponse, error)
	getResourceAllocation(c context.Context, request *pluginapi.GetResourcesAllocationRequest) (*pluginapi.GetResourcesAllocationResponse, error)
	removePod(c context.Context, removePodRequest *pluginapi.RemovePodRequest) (*pluginapi.RemovePodResponse, error)
	isStopped() bool
	stopGracePeriodExpired() bool
}

type endpointImpl struct {
	client     pluginapi.ResourcePluginClient
	clientConn *grpc.ClientConn

	socketPath   string
	resourceName string
	stopTime     time.Time

	mutex sync.Mutex
}

// newEndpointImpl creates a new endpoint for the given resourceName.
// This is to be used during normal resource plugin registration.
func newEndpointImpl(socketPath, resourceName string) (*endpointImpl, error) {
	client, c, err := dial(socketPath)
	if err != nil {
		klog.Errorf("[qosresourcemanager] Can't create new endpoint with path %s err %v", socketPath, err)
		return nil, err
	}

	return &endpointImpl{
		client:     client,
		clientConn: c,

		socketPath:   socketPath,
		resourceName: resourceName,
	}, nil
}

// newStoppedEndpointImpl creates a new endpoint for the given resourceName with stopTime set.
// This is to be used during Kubelet restart, before the actual resource plugin re-registers.
func newStoppedEndpointImpl(resourceName string) *endpointImpl {
	return &endpointImpl{
		resourceName: resourceName,
		stopTime:     time.Now(),
	}
}

func (e *endpointImpl) isStopped() bool {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return !e.stopTime.IsZero()
}

func (e *endpointImpl) stopGracePeriodExpired() bool {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return !e.stopTime.IsZero() && time.Since(e.stopTime) > endpointStopGracePeriod
}

// used for testing only
func (e *endpointImpl) setStopTime(t time.Time) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.stopTime = t
}

// allocate issues Allocate gRPC call to the resource plugin.
func (e *endpointImpl) allocate(c context.Context, resourceRequest *pluginapi.ResourceRequest) (*pluginapi.ResourceAllocationResponse, error) {
	if e.isStopped() {
		return nil, fmt.Errorf(errEndpointStopped, e)
	}
	ctx, cancel := context.WithTimeout(c, pluginapi.KubeletResourcePluginAllocateRPCTimeoutInSecs*time.Second)
	defer cancel()
	return e.client.Allocate(ctx, resourceRequest)
}

// getTopologyHints issues GetTopologyHints gRPC call to the resource plugin.
func (e *endpointImpl) getTopologyHints(c context.Context, resourceRequest *pluginapi.ResourceRequest) (*pluginapi.ResourceHintsResponse, error) {
	if e.isStopped() {
		return nil, fmt.Errorf(errEndpointStopped, e)
	}
	ctx, cancel := context.WithTimeout(c, pluginapi.KubeletResourcePluginGetTopologyHintsRPCTimeoutInSecs*time.Second)
	defer cancel()
	return e.client.GetTopologyHints(ctx, resourceRequest)
}

// preStartContainer issues PreStartContainer gRPC call to the resource plugin.
func (e *endpointImpl) preStartContainer(pod *v1.Pod, container *v1.Container) (*pluginapi.PreStartContainerResponse, error) {
	if e.isStopped() {
		return nil, fmt.Errorf(errEndpointStopped, e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), pluginapi.KubeletResourcePluginPreStartContainerRPCTimeoutInSecs*time.Second)
	defer cancel()
	return e.client.PreStartContainer(ctx, &pluginapi.PreStartContainerRequest{
		PodUid:        string(pod.UID),
		PodNamespace:  pod.Namespace,
		PodName:       pod.Name,
		ContainerName: container.Name,
	})
}

func (e *endpointImpl) stop() {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if e.clientConn != nil {
		e.clientConn.Close()
	}
	e.stopTime = time.Now()
}

func (e *endpointImpl) getTopologyAwareAllocatableResources(c context.Context, request *pluginapi.GetTopologyAwareAllocatableResourcesRequest) (*pluginapi.GetTopologyAwareAllocatableResourcesResponse, error) {
	if e.isStopped() {
		return nil, fmt.Errorf(errEndpointStopped, e)
	}
	ctx, cancel := context.WithTimeout(c, pluginapi.KubeletResourcePluginGetTopologyAwareAllocatableResourcesRPCTimeoutInSecs*time.Second)
	defer cancel()
	return e.client.GetTopologyAwareAllocatableResources(ctx, request)
}

func (e *endpointImpl) getTopologyAwareResources(c context.Context, request *pluginapi.GetTopologyAwareResourcesRequest) (*pluginapi.GetTopologyAwareResourcesResponse, error) {
	if e.isStopped() {
		return nil, fmt.Errorf(errEndpointStopped, e)
	}
	ctx, cancel := context.WithTimeout(c, pluginapi.KubeletResourcePluginGetTopologyAwareResourcesRPCTimeoutInSecs*time.Second)
	defer cancel()
	return e.client.GetTopologyAwareResources(ctx, request)
}

func (e *endpointImpl) getResourceAllocation(c context.Context, request *pluginapi.GetResourcesAllocationRequest) (*pluginapi.GetResourcesAllocationResponse, error) {
	if e.isStopped() {
		return nil, fmt.Errorf(errEndpointStopped, e)
	}
	ctx, cancel := context.WithTimeout(c, pluginapi.KubeletResourcePluginGetResourcesAllocationRPCTimeoutInSecs*time.Second)
	defer cancel()
	return e.client.GetResourcesAllocation(ctx, request)
}

func (e *endpointImpl) removePod(c context.Context, removePodRequest *pluginapi.RemovePodRequest) (*pluginapi.RemovePodResponse, error) {
	if e.isStopped() {
		return nil, fmt.Errorf(errEndpointStopped, e)
	}
	ctx, cancel := context.WithTimeout(c, pluginapi.KubeletResourcePluginRemovePodRPCTimeoutInSecs*time.Second)
	defer cancel()
	return e.client.RemovePod(ctx, removePodRequest)
}

// dial establishes the gRPC communication with the registered resource plugin. https://godoc.org/google.golang.org/grpc#Dial
func dial(unixSocketPath string) (pluginapi.ResourcePluginClient, *grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := grpc.DialContext(ctx, unixSocketPath, grpc.WithInsecure(), grpc.WithBlock(),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", addr)
		}),
	)

	if err != nil {
		return nil, nil, fmt.Errorf(errFailedToDialResourcePlugin+" %v", err)
	}

	return pluginapi.NewResourcePluginClient(c), c, nil
}
