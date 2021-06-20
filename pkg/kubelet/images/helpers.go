/*
Copyright 2016 The Kubernetes Authors.

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

package images

import (
	"fmt"
	"sync/atomic"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/flowcontrol"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
)

// throttleImagePulling wraps kubecontainer.ImageService to throttle image
// pulling based on the given QPS and burst limits. If QPS is zero, defaults
// to no throttling.
func throttleImagePulling(imageService kubecontainer.ImageService, qps float32, burst int, maxConcurrency int) kubecontainer.ImageService {
	if qps == 0.0 {
		return imageService
	}
	klog.V(2).Infof("build throttled image pulling helper with QPS: %f, burst: %d, maxConcurrency: %d", qps, burst, maxConcurrency)
	return &throttledImageService{
		ImageService:   imageService,
		limiter:        flowcontrol.NewTokenBucketRateLimiter(qps, burst),
		maxConcurrency: int32(maxConcurrency),
		concurrency:    0,
	}
}

type throttledImageService struct {
	kubecontainer.ImageService
	limiter        flowcontrol.RateLimiter
	concurrency    int32
	maxConcurrency int32
}

func (ts throttledImageService) PullImage(image kubecontainer.ImageSpec, secrets []v1.Secret, podSandboxConfig *runtimeapi.PodSandboxConfig) (string, error) {
	if ts.maxConcurrency > 0 {
		defer func() {
			atomic.AddInt32(&ts.concurrency, -1)
		}()
		concurrency := atomic.AddInt32(&ts.concurrency, 1)
		if concurrency > ts.maxConcurrency {
			klog.V(4).Infof("reject image pull for max concurrency [%d/%d] exceeded.", concurrency, ts.maxConcurrency)
			return "", fmt.Errorf("pull concurrency exceeded")
		}
		klog.V(5).Infof("do image pull for concurrency [%d/%d]", concurrency, ts.maxConcurrency)
	}
	if ts.limiter.TryAccept() {
		return ts.ImageService.PullImage(image, secrets, podSandboxConfig)
	}
	return "", fmt.Errorf("pull QPS exceeded")
}
