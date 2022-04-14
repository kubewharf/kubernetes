// +build linux

/*
Copyright 2018 The Kubernetes Authors.

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

package kuberuntime

import (
	"fmt"
	"time"

	cgroupfs "github.com/opencontainers/runc/libcontainer/cgroups/fs"
	v1 "k8s.io/api/core/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	kubefeatures "k8s.io/kubernetes/pkg/features"
	"k8s.io/kubernetes/pkg/kubelet/config"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	"k8s.io/kubernetes/pkg/kubelet/qos"
	"k8s.io/kubernetes/pkg/kubelet/types"
)

// applyPlatformSpecificContainerConfig applies platform specific configurations to runtimeapi.ContainerConfig.
func (m *kubeGenericRuntimeManager) applyPlatformSpecificContainerConfig(config *runtimeapi.ContainerConfig, container *v1.Container, pod *v1.Pod, uid *int64, username string, nsTarget *kubecontainer.ContainerID) error {
	if config == nil {
		return fmt.Errorf("applyPlatformSpecificContainerConfig met nil input config")
	}

	if pod == nil || container == nil {
		return fmt.Errorf("applyPlatformSpecificContainerConfig met nil pod or container")
	}

	var opts *kubecontainer.ResourceRunContainerOptions
	if utilfeature.DefaultFeatureGate.Enabled(kubefeatures.QoSResourceManager) {
		var err error
		opts, err = m.runtimeHelper.GenerateResourceRunContainerOptions(pod, container)

		if err != nil {
			klog.Errorf("[applyPlatformSpecificContainerConfig] pod: %s/%s, containerName: %s GenerateResourceRunContainerOptions failed with error: %v",
				pod.Namespace, pod.Name, container.Name, err)
			return fmt.Errorf("GenerateResourceRunContainerOptions failed with error: %v", err)
		}

		if config.Annotations == nil {
			config.Annotations = make(map[string]string)
		}

		if opts != nil {
			for _, anno := range opts.Annotations {
				config.Annotations[anno.Name] = anno.Value
			}

			for _, env := range opts.Envs {
				config.Envs = append(config.Envs, &runtimeapi.KeyValue{
					Key:   env.Name,
					Value: env.Value,
				})
			}
		}
	}

	config.Linux = m.generateLinuxContainerConfig(container, pod, uid, username, nsTarget, opts, config.Annotations)
	return nil
}

// generateLinuxContainerConfig generates linux container config for kubelet runtime v1.
func (m *kubeGenericRuntimeManager) generateLinuxContainerConfig(container *v1.Container, pod *v1.Pod, uid *int64, username string, nsTarget *kubecontainer.ContainerID, opts *kubecontainer.ResourceRunContainerOptions, configAnnotations map[string]string) *runtimeapi.LinuxContainerConfig {

	resourceConfig := &runtimeapi.LinuxContainerResources{}

	if utilfeature.DefaultFeatureGate.Enabled(kubefeatures.QoSResourceManager) && opts != nil && opts.Resources != nil {
		resourceConfig = opts.Resources
	}

	// TODO(sunjianyu): consider if we should make results from qos resource manager override native action results?
	lc := &runtimeapi.LinuxContainerConfig{
		Resources:       resourceConfig,
		SecurityContext: m.determineEffectiveSecurityContext(pod, container, uid, username),
	}

	if nsTarget != nil && lc.SecurityContext.NamespaceOptions.Pid == runtimeapi.NamespaceMode_CONTAINER {
		lc.SecurityContext.NamespaceOptions.Pid = runtimeapi.NamespaceMode_TARGET
		lc.SecurityContext.NamespaceOptions.TargetId = nsTarget.ID
	}

	// set linux container resources
	var cpuShares int64
	cpuRequest := container.Resources.Requests.Cpu()
	cpuLimit := container.Resources.Limits.Cpu()
	memoryLimit := container.Resources.Limits.Memory().Value()
	oomScoreAdj := int64(qos.GetContainerOOMScoreAdjust(pod, container,
		int64(m.machineInfo.MemoryCapacity)))
	// If pod specify resource-type as best-effort explicitly, use min-shares. Otherwise,
	// if request is not specified, but limit is, we want request to default to limit.
	// API server does this for new containers, but we repeat this logic in Kubelet
	// for containers running on existing Kubernetes clusters.
	if pod.Annotations[v1.PodResourceTypeAnnotationKey] == v1.ResourceTypeBestEffort {
		cpuShares = minShares
	} else {
		if cpuRequest.IsZero() && !cpuLimit.IsZero() {
			cpuShares = milliCPUToShares(cpuLimit.MilliValue())
		} else {
			burstCpuRequest, err := types.GetBurstRequest(pod, v1.ResourceCPU)
			if err == nil && burstCpuRequest.Cmp(*cpuRequest) != 0 {
				cpuRequest = burstCpuRequest
				klog.V(2).Infof("cpu request of pod %s(%s) is bursted to %d", pod.Name, pod.UID, burstCpuRequest.MilliValue())
			}
			// if cpuRequest.Amount is nil, then milliCPUToShares will return the minimal number
			// of CPU shares.
			cpuShares = milliCPUToShares(cpuRequest.MilliValue())
		}
	}

	lc.Resources.CpuShares = cpuShares
	if memoryLimit != 0 {
		lc.Resources.MemoryLimitInBytes = memoryLimit
	}
	// Set OOM score of the container based on qos policy. Processes in lower-priority pods should
	// be killed first if the system runs out of memory.
	lc.Resources.OomScoreAdj = oomScoreAdj

	if m.cpuCFSQuota {
		cpuPeriod := int64(defaultQuotaPeriod)

		// firstly, use pod-level quota period if exists;
		// secondly, use node-level quota period if exists;
		// otherwise, use default quota period.
		quotaPeriod, err := types.GetCpuCfsQuotaPeriod(pod)
		if err == nil {
			cpuPeriod = int64(quotaPeriod)
		} else if utilfeature.DefaultFeatureGate.Enabled(kubefeatures.CPUCFSQuotaPeriod) {
			cpuPeriod = int64(m.cpuCFSQuotaPeriod.Duration / time.Microsecond)
		}

		cpuQuota := milliCPUToQuota(cpuLimit.MilliValue(), cpuPeriod)
		lc.Resources.CpuQuota = cpuQuota
		lc.Resources.CpuPeriod = cpuPeriod
	}

	lc.Resources.HugepageLimits = GetHugepageLimitsFromResources(container.Resources)

	if configAnnotations[config.CPUSetAnnotation] != "" {
		klog.V(2).Infof("cpuset has been set for pod: %s, container: %s, cpuset: %s", pod.Name, container.Name, configAnnotations[config.CPUSetAnnotation])
		lc.Resources.CpusetCpus = configAnnotations[config.CPUSetAnnotation]
	}

	if configAnnotations[config.NumaSetAnnotation] != "" {
		klog.V(2).Infof("numaset has been set for pod: %s, container: %s, numaset: %s", pod.Name, container.Name, configAnnotations[config.NumaSetAnnotation])
		lc.Resources.CpusetMems = configAnnotations[config.NumaSetAnnotation]
	}

	return lc
}

// GetHugepageLimitsFromResources returns limits of each hugepages from resources.
func GetHugepageLimitsFromResources(resources v1.ResourceRequirements) []*runtimeapi.HugepageLimit {
	var hugepageLimits []*runtimeapi.HugepageLimit

	// For each page size, limit to 0.
	for _, pageSize := range cgroupfs.HugePageSizes {
		hugepageLimits = append(hugepageLimits, &runtimeapi.HugepageLimit{
			PageSize: pageSize,
			Limit:    uint64(0),
		})
	}

	requiredHugepageLimits := map[string]uint64{}
	for resourceObj, amountObj := range resources.Limits {
		if !v1helper.IsHugePageResourceName(resourceObj) {
			continue
		}

		pageSize, err := v1helper.HugePageSizeFromResourceName(resourceObj)
		if err != nil {
			klog.Warningf("Failed to get hugepage size from resource name: %v", err)
			continue
		}

		sizeString, err := v1helper.HugePageUnitSizeFromByteSize(pageSize.Value())
		if err != nil {
			klog.Warningf("pageSize is invalid: %v", err)
			continue
		}
		requiredHugepageLimits[sizeString] = uint64(amountObj.Value())
	}

	for _, hugepageLimit := range hugepageLimits {
		if limit, exists := requiredHugepageLimits[hugepageLimit.PageSize]; exists {
			hugepageLimit.Limit = limit
		}
	}

	return hugepageLimits
}
