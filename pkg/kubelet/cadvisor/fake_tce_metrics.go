// +Fbuild cgo linux darwin

/*
Copyright 2015 The Kubernetes Authors.

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

package cadvisor

import (
	"k8s.io/klog"
)

type fakeClient struct{}

func NewFakeTCEMetricsClient() TCEInterface {
	return &fakeClient{}
}

func (tmc *fakeClient) Start() error {
	klog.Info("[fake client] Start()")
	return nil
}

func (tmc *fakeClient) Stop() error {
	klog.Info("[fake client] Stop()")
	return nil
}

func (tmc *fakeClient) ThresholdsMet(softLimit int64, hardLimit int64) (bool, bool) {
	klog.Info("[fake client] ThresholdsMet()")
	return false, false
}

func (tmc *fakeClient) GetLoad(podname string) float64 {
	klog.Info("[fake client] GetLoad()")
	return 0.0
}
