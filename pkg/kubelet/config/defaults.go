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

package config

import kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"

// Defines sane defaults for the kubelet config.
const (
	DefaultKubeletPodsDirName                = "pods"
	DefaultKubeletVolumesDirName             = "volumes"
	DefaultKubeletVolumeSubpathsDirName      = "volume-subpaths"
	DefaultKubeletVolumeDevicesDirName       = "volumeDevices"
	DefaultKubeletPluginsDirName             = "plugins"
	DefaultKubeletPluginsRegistrationDirName = "plugins_registry"
	DefaultKubeletContainersDirName          = "containers"
	DefaultKubeletPluginContainersDirName    = "plugin-containers"
	DefaultKubeletPodResourcesDirName        = "pod-resources"
	KubeletPluginsDirSELinuxLabel            = "system_u:object_r:container_file_t:s0"

	CPUSetAnnotation  = "bytedance.com/cpuset"
	NumaSetAnnotation = "bytedance.com/numaset"

	PodRoleLabel = "bytedance.com/pod-role"

	UserNameAnnotation        = "godel.bytedance.com/user-name"
	SSDAffinityAnnotation     = "godel.bytedance.com/ssd-affinity"
	// NICAffinityAnnotationName is the same as the constant of kubecontainer for not leading to cycle import
	NICAffinityAnnotationName = kubecontainer.NICAffinityAnnotationName
	ApplicationNameLabel      = "godel.bytedance.com/application-name"
)
