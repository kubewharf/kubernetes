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

package util

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilpod "k8s.io/kubernetes/pkg/api/pod"
)

// FromApiserverCache modifies <opts> so that the GET request will
// be served from apiserver cache instead of from etcd.
func FromApiserverCache(opts *metav1.GetOptions) {
	opts.ResourceVersion = "0"
}

func AdmitForKubelet(pod *v1.Pod) bool {
	if !utilpod.LauncherIsSet(pod.Annotations) {
		return true
	}
	if utilpod.LauncherIsNodeManager(pod.Annotations) {
		return false
	}
	if !utilpod.LauncherIsKubelet(pod.Annotations) {
		return false
	}
	return true
}