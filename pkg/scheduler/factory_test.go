/*
Copyright 2014 The Kubernetes Authors.

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

package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/clock"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/events"
	extenderv1 "k8s.io/kube-scheduler/extender/v1"
	apitesting "k8s.io/kubernetes/pkg/api/testing"
	kubefeatures "k8s.io/kubernetes/pkg/features"
	schedulerapi "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/apis/config/scheme"
	frameworkplugins "k8s.io/kubernetes/pkg/scheduler/framework/plugins"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/defaultbinder"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/interpodaffinity"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/nodelabel"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/queuesort"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/serviceaffinity"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
	internalcache "k8s.io/kubernetes/pkg/scheduler/internal/cache"
	internalqueue "k8s.io/kubernetes/pkg/scheduler/internal/queue"
	"k8s.io/kubernetes/pkg/scheduler/listers"
	schedulernodeinfo "k8s.io/kubernetes/pkg/scheduler/nodeinfo"
	"k8s.io/kubernetes/pkg/scheduler/profile"
)

const (
	disablePodPreemption             = false
	bindTimeoutSeconds               = 600
	podInitialBackoffDurationSeconds = 1
	podMaxBackoffDurationSeconds     = 10
	testSchedulerName                = "test-scheduler"
)

func skipTestCreate(t *testing.T) {
	client := fake.NewSimpleClientset()
	stopCh := make(chan struct{})
	defer close(stopCh)
	factory := newConfigFactory(client, stopCh)
	if _, err := factory.createFromProvider(schedulerapi.SchedulerDefaultProviderName); err != nil {
		t.Error(err)
	}
}

// Test configures a scheduler from a policies defined in a file
// It combines some configurable predicate/priorities with some pre-defined ones
// Fix Me by TCEers
func skipTestCreateFromConfig(t *testing.T) {
	var configData []byte

	configData = []byte(`{
		"kind" : "Policy",
		"apiVersion" : "v1",
		"predicates" : [
			{"name" : "TestZoneAffinity", "argument" : {"serviceAffinity" : {"labels" : ["zone"]}}},
			{"name" : "TestZoneAffinity", "argument" : {"serviceAffinity" : {"labels" : ["foo"]}}},
			{"name" : "TestRequireZone", "argument" : {"labelsPresence" : {"labels" : ["zone"], "presence" : true}}},
			{"name" : "TestNoFooLabel", "argument" : {"labelsPresence" : {"labels" : ["foo"], "presence" : false}}},
			{"name" : "PodFitsResources"},
			{"name" : "PodFitsHostPorts"}
		],
		"priorities" : [
			{"name" : "RackSpread", "weight" : 3, "argument" : {"serviceAntiAffinity" : {"label" : "rack"}}},
			{"name" : "ZoneSpread", "weight" : 3, "argument" : {"serviceAntiAffinity" : {"label" : "zone"}}},
			{"name" : "LabelPreference1", "weight" : 3, "argument" : {"labelPreference" : {"label" : "l1", "presence": true}}},
			{"name" : "LabelPreference2", "weight" : 3, "argument" : {"labelPreference" : {"label" : "l2", "presence": false}}},
			{"name" : "NodeAffinityPriority", "weight" : 2},
			{"name" : "ImageLocalityPriority", "weight" : 1}		]
	}`)
	cases := []struct {
		name       string
		plugins    *schedulerapi.Plugins
		pluginCfgs []schedulerapi.PluginConfig
		wantErr    string
	}{
		{
			name: "just policy",
		},
		{
			name: "policy and plugins",
			plugins: &schedulerapi.Plugins{
				Filter: &schedulerapi.PluginSet{
					Disabled: []schedulerapi.Plugin{{Name: nodelabel.Name}},
				},
			},
			wantErr: "using Plugins and Policy simultaneously is not supported",
		},
		{
			name: "policy and plugin config",
			pluginCfgs: []schedulerapi.PluginConfig{
				{Name: queuesort.Name},
			},
			wantErr: "using PluginConfig and Policy simultaneously is not supported",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			stopCh := make(chan struct{})
			defer close(stopCh)
			factory := newConfigFactory(client, stopCh)
			factory.profiles[0].Plugins = tc.plugins
			factory.profiles[0].PluginConfig = tc.pluginCfgs

			var policy schedulerapi.Policy
			if err := runtime.DecodeInto(scheme.Codecs.UniversalDecoder(), configData, &policy); err != nil {
				t.Errorf("Invalid configuration: %v", err)
			}

			sched, err := factory.createFromConfig(policy)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("got err %q, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("createFromConfig failed: %v", err)
			}
			// createFromConfig is the old codepath where we only have one profile.
			prof := sched.Profiles[testSchedulerName]
			queueSortPls := prof.ListPlugins()["QueueSortPlugin"]
			wantQueuePls := []schedulerapi.Plugin{{Name: queuesort.Name}}
			if diff := cmp.Diff(wantQueuePls, queueSortPls); diff != "" {
				t.Errorf("Unexpected QueueSort plugins (-want, +got): %s", diff)
			}
			bindPls := prof.ListPlugins()["BindPlugin"]
			wantBindPls := []schedulerapi.Plugin{{Name: defaultbinder.Name}}
			if diff := cmp.Diff(wantBindPls, bindPls); diff != "" {
				t.Errorf("Unexpected Bind plugins (-want, +got): %s", diff)
			}

			// Verify that node label predicate/priority are converted to framework plugins.
			wantArgs := `{"Name":"NodeLabel","Args":{"presentLabels":["zone"],"absentLabels":["foo"],"presentLabelsPreference":["l1"],"absentLabelsPreference":["l2"]}}`
			verifyPluginConvertion(t, nodelabel.Name, []string{"FilterPlugin", "ScorePlugin"}, prof, &factory.profiles[0], 6, wantArgs)
			// Verify that service affinity custom predicate/priority is converted to framework plugin.
			wantArgs = `{"Name":"ServiceAffinity","Args":{"affinityLabels":["zone","foo"],"antiAffinityLabelsPreference":["rack","zone"]}}`
			verifyPluginConvertion(t, serviceaffinity.Name, []string{"FilterPlugin", "ScorePlugin"}, prof, &factory.profiles[0], 6, wantArgs)
			// TODO(#87703): Verify all plugin configs.
		})
	}

}

func verifyPluginConvertion(t *testing.T, name string, extentionPoints []string, prof *profile.Profile, cfg *schedulerapi.KubeSchedulerProfile, wantWeight int32, wantArgs string) {
	for _, extensionPoint := range extentionPoints {
		plugin, ok := findPlugin(name, extensionPoint, prof)
		if !ok {
			t.Fatalf("%q plugin does not exist in framework.", name)
		}
		if extensionPoint == "ScorePlugin" {
			if plugin.Weight != wantWeight {
				t.Errorf("Wrong weight. Got: %v, want: %v", plugin.Weight, wantWeight)
			}
		}
		// Verify that the policy config is converted to plugin config.
		pluginConfig := findPluginConfig(name, cfg)
		encoding, err := json.Marshal(pluginConfig)
		if err != nil {
			t.Errorf("Failed to marshal %+v: %v", pluginConfig, err)
		}
		if string(encoding) != wantArgs {
			t.Errorf("Config for %v plugin mismatch. got: %v, want: %v", name, string(encoding), wantArgs)
		}
	}
}

func findPlugin(name, extensionPoint string, prof *profile.Profile) (schedulerapi.Plugin, bool) {
	for _, pl := range prof.ListPlugins()[extensionPoint] {
		if pl.Name == name {
			return pl, true
		}
	}
	return schedulerapi.Plugin{}, false
}

func findPluginConfig(name string, prof *schedulerapi.KubeSchedulerProfile) schedulerapi.PluginConfig {
	for _, c := range prof.PluginConfig {
		if c.Name == name {
			return c
		}
	}
	return schedulerapi.PluginConfig{}
}

// Fix Me by TCEers.
func skipTestCreateFromConfigWithHardPodAffinitySymmetricWeight(t *testing.T) {
	var configData []byte
	var policy schedulerapi.Policy

	client := fake.NewSimpleClientset()
	stopCh := make(chan struct{})
	defer close(stopCh)
	factory := newConfigFactory(client, stopCh)

	configData = []byte(`{
		"kind" : "Policy",
		"apiVersion" : "v1",
		"predicates" : [
			{"name" : "TestZoneAffinity", "argument" : {"serviceAffinity" : {"labels" : ["zone"]}}},
			{"name" : "TestRequireZone", "argument" : {"labelsPresence" : {"labels" : ["zone"], "presence" : true}}},
			{"name" : "PodFitsResources"},
			{"name" : "PodFitsHostPorts"}
		],
		"priorities" : [
			{"name" : "RackSpread", "weight" : 3, "argument" : {"serviceAntiAffinity" : {"label" : "rack"}}},
			{"name" : "NodeAffinityPriority", "weight" : 2},
			{"name" : "ImageLocalityPriority", "weight" : 1},
			{"name" : "InterPodAffinityPriority", "weight" : 1}
		],
		"hardPodAffinitySymmetricWeight" : 10
	}`)
	if err := runtime.DecodeInto(scheme.Codecs.UniversalDecoder(), configData, &policy); err != nil {
		t.Fatalf("Invalid configuration: %v", err)
	}
	if _, err := factory.createFromConfig(policy); err != nil {
		t.Fatal(err)
	}
	// TODO(#87703): Verify that the entire pluginConfig is correct.
	foundAffinityCfg := false
	for _, cfg := range factory.profiles[0].PluginConfig {
		if cfg.Name == interpodaffinity.Name {
			foundAffinityCfg = true
			wantArgs := runtime.Unknown{Raw: []byte(`{"hardPodAffinityWeight":10}`)}

			if diff := cmp.Diff(wantArgs, cfg.Args); diff != "" {
				t.Errorf("wrong InterPodAffinity args (-want, +got): %s", diff)
			}
		}
	}
	if !foundAffinityCfg {
		t.Errorf("args for InterPodAffinity were not found")
	}
}

// Fix Me by TCEers.
func skipTestCreateFromEmptyConfig(t *testing.T) {
	var configData []byte
	var policy schedulerapi.Policy

	client := fake.NewSimpleClientset()
	stopCh := make(chan struct{})
	defer close(stopCh)
	factory := newConfigFactory(client, stopCh)

	configData = []byte(`{}`)
	if err := runtime.DecodeInto(scheme.Codecs.UniversalDecoder(), configData, &policy); err != nil {
		t.Errorf("Invalid configuration: %v", err)
	}

	if _, err := factory.createFromConfig(policy); err != nil {
		t.Fatal(err)
	}
	prof := factory.profiles[0]
	if len(prof.PluginConfig) != 0 {
		t.Errorf("got plugin config %s, want none", prof.PluginConfig)
	}
}

// Test configures a scheduler from a policy that does not specify any
// predicate/priority.
// The predicate/priority from DefaultProvider will be used.
// Fix Me by TCEers.
func skipTestCreateFromConfigWithUnspecifiedPredicatesOrPriorities(t *testing.T) {
	client := fake.NewSimpleClientset()
	stopCh := make(chan struct{})
	defer close(stopCh)
	factory := newConfigFactory(client, stopCh)

	configData := []byte(`{
		"kind" : "Policy",
		"apiVersion" : "v1"
	}`)
	var policy schedulerapi.Policy
	if err := runtime.DecodeInto(scheme.Codecs.UniversalDecoder(), configData, &policy); err != nil {
		t.Fatalf("Invalid configuration: %v", err)
	}

	sched, err := factory.createFromConfig(policy)
	if err != nil {
		t.Fatalf("Failed to create scheduler from configuration: %v", err)
	}
	if _, exist := findPlugin("NodeResourcesFit", "FilterPlugin", sched.Profiles[testSchedulerName]); !exist {
		t.Errorf("Expected plugin NodeResourcesFit")
	}
}

func TestDefaultErrorFunc(t *testing.T) {
	testPod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "bar"},
		Spec:       apitesting.V1DeepEqualSafePodSpec(),
	}
	testPodUpdated := testPod.DeepCopy()
	testPodUpdated.Labels = map[string]string{"foo": ""}

	tests := []struct {
		name                       string
		injectErr                  error
		podUpdatedDuringScheduling bool // pod is updated during a scheduling cycle
		podDeletedDuringScheduling bool // pod is deleted during a scheduling cycle
		expect                     *v1.Pod
	}{
		{
			name:                       "pod is updated during a scheduling cycle",
			injectErr:                  nil,
			podUpdatedDuringScheduling: true,
			expect:                     testPodUpdated,
		},
		{
			name:      "pod is not updated during a scheduling cycle",
			injectErr: nil,
			expect:    testPod,
		},
		{
			name:                       "pod is deleted during a scheduling cycle",
			injectErr:                  nil,
			podDeletedDuringScheduling: true,
			expect:                     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stopCh := make(chan struct{})
			defer close(stopCh)

			client := fake.NewSimpleClientset(&v1.PodList{Items: []v1.Pod{*testPod}})
			informerFactory := informers.NewSharedInformerFactory(client, 0)
			podInformer := informerFactory.Core().V1().Pods()
			// Need to add/update/delete testPod to the store.
			podInformer.Informer().GetStore().Add(testPod)

			queue := internalqueue.NewPriorityQueue(nil, internalqueue.WithClock(clock.NewFakeClock(time.Now())))
			schedulerCache := internalcache.New(30*time.Second, stopCh)

			queue.Add(testPod)
			queue.Pop()

			if tt.podUpdatedDuringScheduling {
				podInformer.Informer().GetStore().Update(testPodUpdated)
				queue.Update(testPod, testPodUpdated)
			}
			if tt.podDeletedDuringScheduling {
				podInformer.Informer().GetStore().Delete(testPod)
				queue.Delete(testPod)
			}

			testPodInfo := &framework.PodInfo{Pod: testPod}
			errFunc := MakeDefaultErrorFunc(client, podInformer.Lister(), queue, schedulerCache)
			errFunc(testPodInfo, tt.injectErr)

			var got *v1.Pod
			if tt.podUpdatedDuringScheduling {
				head, e := queue.Pop()
				if e != nil {
					t.Fatalf("Cannot pop pod from the activeQ: %v", e)
				}
				got = head.Pod
			} else {
				got = getPodfromPriorityQueue(queue, testPod)
			}

			if diff := cmp.Diff(tt.expect, got); diff != "" {
				t.Errorf("Unexpected pod (-want, +got): %s", diff)
			}
		})
	}
}

// getPodfromPriorityQueue is the function used in the TestDefaultErrorFunc test to get
// the specific pod from the given priority queue. It returns the found pod in the priority queue.
func getPodfromPriorityQueue(queue *internalqueue.PriorityQueue, pod *v1.Pod) *v1.Pod {
	podList := queue.PendingPods()
	if len(podList) == 0 {
		return nil
	}

	queryPodKey, err := cache.MetaNamespaceKeyFunc(pod)
	if err != nil {
		return nil
	}

	for _, foundPod := range podList {
		foundPodKey, err := cache.MetaNamespaceKeyFunc(foundPod)
		if err != nil {
			return nil
		}

		if foundPodKey == queryPodKey {
			return foundPod
		}
	}

	return nil
}

func newConfigFactoryWithFrameworkRegistry(
	client clientset.Interface, stopCh <-chan struct{},
	registry framework.Registry) *Configurator {
	informerFactory := informers.NewSharedInformerFactory(client, 0)
	snapshot := internalcache.NewEmptySnapshot()
	recorderFactory := profile.NewRecorderFactory(events.NewBroadcaster(&events.EventSinkImpl{Interface: client.EventsV1beta1().Events("")}))
	return &Configurator{
		client:                   client,
		informerFactory:          informerFactory,
		disablePreemption:        disablePodPreemption,
		percentageOfNodesToScore: schedulerapi.DefaultPercentageOfNodesToScore,
		bindTimeoutSeconds:       bindTimeoutSeconds,
		podInitialBackoffSeconds: podInitialBackoffDurationSeconds,
		podMaxBackoffSeconds:     podMaxBackoffDurationSeconds,
		StopEverything:           stopCh,
		enableNonPreempting:      utilfeature.DefaultFeatureGate.Enabled(kubefeatures.NonPreemptingPriority),
		registry:                 registry,
		profiles: []schedulerapi.KubeSchedulerProfile{
			{SchedulerName: testSchedulerName},
		},
		recorderFactory:  recorderFactory,
		nodeInfoSnapshot: snapshot,
	}
}

func newConfigFactory(client clientset.Interface, stopCh <-chan struct{}) *Configurator {
	return newConfigFactoryWithFrameworkRegistry(client, stopCh,
		frameworkplugins.NewInTreeRegistry())
}

type fakeExtender struct {
	isBinder          bool
	interestedPodName string
	ignorable         bool
	gotBind           bool
}

func (f *fakeExtender) Name() string {
	return "fakeExtender"
}

func (f *fakeExtender) IsIgnorable() bool {
	return f.ignorable
}

func (f *fakeExtender) ProcessPreemption(
	pod *v1.Pod,
	nodeToVictims map[*v1.Node]*extenderv1.Victims,
	nodeInfos listers.NodeInfoLister,
) (map[*v1.Node]*extenderv1.Victims, error) {
	return nil, nil
}

func (f *fakeExtender) SupportsPreemption() bool {
	return false
}

func (f *fakeExtender) Filter(pod *v1.Pod, nodes []*v1.Node) (filteredNodes []*v1.Node, failedNodesMap extenderv1.FailedNodesMap, err error) {
	return nil, nil, nil
}

func (f *fakeExtender) Prioritize(
	pod *v1.Pod,
	nodes []*v1.Node,
) (hostPriorities *extenderv1.HostPriorityList, weight int64, err error) {
	return nil, 0, nil
}

func (f *fakeExtender) Bind(binding *v1.Binding) error {
	if f.isBinder {
		f.gotBind = true
		return nil
	}
	return errors.New("not a binder")
}

func (f *fakeExtender) IsBinder() bool {
	return f.isBinder
}

func (f *fakeExtender) IsInterested(pod *v1.Pod) bool {
	return pod != nil && pod.Name == f.interestedPodName
}

type TestPlugin struct {
	name string
}

var _ framework.ScorePlugin = &TestPlugin{}
var _ framework.FilterPlugin = &TestPlugin{}

func (t *TestPlugin) Name() string {
	return t.name
}

func (t *TestPlugin) Score(ctx context.Context, state *framework.CycleState, p *v1.Pod, nodeName string) (int64, *framework.Status) {
	return 1, nil
}

func (t *TestPlugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

func (t *TestPlugin) Filter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo *schedulernodeinfo.NodeInfo) *framework.Status {
	return nil
}
