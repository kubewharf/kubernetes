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
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"time"

	bytedinformers "code.byted.org/kubernetes/clientsets/k8s/informers"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/kubernetes/pkg/controller/volume/scheduling"
	"k8s.io/kubernetes/pkg/features"
	kubefeatures "k8s.io/kubernetes/pkg/features"
	schedulerapi "k8s.io/kubernetes/pkg/scheduler/apis/config"
	"k8s.io/kubernetes/pkg/scheduler/apis/config/scheme"
	"k8s.io/kubernetes/pkg/scheduler/core"
	frameworkplugins "k8s.io/kubernetes/pkg/scheduler/framework/plugins"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
	internalcache "k8s.io/kubernetes/pkg/scheduler/internal/cache"
	internalqueue "k8s.io/kubernetes/pkg/scheduler/internal/queue"
	"k8s.io/kubernetes/pkg/scheduler/metrics"
	"k8s.io/kubernetes/pkg/scheduler/profile"
	"k8s.io/kubernetes/pkg/scheduler/util"
)

const (
	// BindTimeoutSeconds defines the default bind timeout
	BindTimeoutSeconds = 100
	// SchedulerError is the reason recorded for events when an error occurs during scheduling a pod.
	SchedulerError = "SchedulerError"
	// Percentage of plugin metrics to be sampled.
	pluginMetricsSamplePercent = 10
)

// podConditionUpdater updates the condition of a pod based on the passed
// PodCondition
// TODO (ahmad-diaa): Remove type and replace it with scheduler methods
type podConditionUpdater interface {
	update(pod *v1.Pod, podCondition *v1.PodCondition) error
}

type podUpdater interface {
	update(pod *v1.Pod) error
}

// PodPreemptor has methods needed to delete a pod and to update 'NominatedPod'
// field of the preemptor pod.
// TODO (ahmad-diaa): Remove type and replace it with scheduler methods
type podPreemptor interface {
	getUpdatedPod(pod *v1.Pod) (*v1.Pod, error)
	deletePod(pod *v1.Pod) error
	setNominatedNodeName(pod *v1.Pod, nominatedNode string) error
	removeNominatedNodeName(pod *v1.Pod) error
}

// Scheduler watches for new unscheduled pods. It attempts to find
// nodes that they fit on and writes bindings back to the api server.
type Scheduler struct {
	// It is expected that changes made via SchedulerCache will be observed
	// by NodeLister and Algorithm.
	SchedulerCache internalcache.Cache

	Algorithm core.ScheduleAlgorithm
	// PodConditionUpdater is used only in case of scheduling errors. If we succeed
	// with scheduling, PodScheduled condition will be updated in apiserver in /bind
	// handler so that binding and setting PodCondition it is atomic.
	podConditionUpdater podConditionUpdater
	// PodPreemptor is used to evict pods and update 'NominatedNode' field of
	// the preemptor pod.
	podPreemptor podPreemptor

	// NextPod should be a function that blocks until the next pod
	// is available. We don't use a channel for this, because scheduling
	// a pod may take some amount of time and we don't want pods to get
	// stale while they sit in a channel.
	NextPod func() *framework.PodInfo

	// Error is called if there is an error. It is passed the pod in
	// question, and the error
	Error func(*framework.PodInfo, error)

	// Close this to shut down the scheduler.
	StopEverything <-chan struct{}

	// VolumeBinder handles PVC/PV binding for the pod.
	VolumeBinder scheduling.SchedulerVolumeBinder

	// Disable pod preemption or not.
	DisablePreemption bool

	// SchedulingQueue holds pods to be scheduled
	SchedulingQueue internalqueue.SchedulingQueue

	podUpdater podUpdater

	// Profiles are the scheduling profiles.
	Profiles profile.Map

	scheduledPodsHasSynced func() bool

	scheduledPodLister corelisters.PodLister
}

// Cache returns the cache in scheduler for test to check the data in scheduler.
func (sched *Scheduler) Cache() internalcache.Cache {
	return sched.SchedulerCache
}

type schedulerOptions struct {
	schedulerAlgorithmSource schedulerapi.SchedulerAlgorithmSource
	disablePreemption        bool
	percentageOfNodesToScore int32
	bindTimeoutSeconds       int64
	podInitialBackoffSeconds int64
	podMaxBackoffSeconds     int64
	// Contains out-of-tree plugins to be merged with the in-tree registry.
	frameworkOutOfTreeRegistry framework.Registry
	profiles                   []schedulerapi.KubeSchedulerProfile
	extenders                  []schedulerapi.Extender

	nodePackageResourceMatchFactor float64
}

// Option configures a Scheduler
type Option func(*schedulerOptions)

// WithProfiles sets profiles for Scheduler. By default, there is one profile
// with the name "default-scheduler".
func WithProfiles(p ...schedulerapi.KubeSchedulerProfile) Option {
	return func(o *schedulerOptions) {
		o.profiles = p
	}
}

// WithAlgorithmSource sets schedulerAlgorithmSource for Scheduler, the default is a source with DefaultProvider.
func WithAlgorithmSource(source schedulerapi.SchedulerAlgorithmSource) Option {
	return func(o *schedulerOptions) {
		o.schedulerAlgorithmSource = source
	}
}

// WithPreemptionDisabled sets disablePreemption for Scheduler, the default value is false
func WithPreemptionDisabled(disablePreemption bool) Option {
	return func(o *schedulerOptions) {
		o.disablePreemption = disablePreemption
	}
}

// WithPercentageOfNodesToScore sets percentageOfNodesToScore for Scheduler, the default value is 50
func WithPercentageOfNodesToScore(percentageOfNodesToScore int32) Option {
	return func(o *schedulerOptions) {
		o.percentageOfNodesToScore = percentageOfNodesToScore
	}
}

func WithNodePackageResourceMatchFactor(nodePackageResourceMatchFactor float64) Option {
	return func(o *schedulerOptions) {
		o.nodePackageResourceMatchFactor = nodePackageResourceMatchFactor
	}
}

// WithBindTimeoutSeconds sets bindTimeoutSeconds for Scheduler, the default value is 100
func WithBindTimeoutSeconds(bindTimeoutSeconds int64) Option {
	return func(o *schedulerOptions) {
		o.bindTimeoutSeconds = bindTimeoutSeconds
	}
}

// WithFrameworkOutOfTreeRegistry sets the registry for out-of-tree plugins. Those plugins
// will be appended to the default registry.
func WithFrameworkOutOfTreeRegistry(registry framework.Registry) Option {
	return func(o *schedulerOptions) {
		o.frameworkOutOfTreeRegistry = registry
	}
}

// WithPodInitialBackoffSeconds sets podInitialBackoffSeconds for Scheduler, the default value is 1
func WithPodInitialBackoffSeconds(podInitialBackoffSeconds int64) Option {
	return func(o *schedulerOptions) {
		o.podInitialBackoffSeconds = podInitialBackoffSeconds
	}
}

// WithPodMaxBackoffSeconds sets podMaxBackoffSeconds for Scheduler, the default value is 10
func WithPodMaxBackoffSeconds(podMaxBackoffSeconds int64) Option {
	return func(o *schedulerOptions) {
		o.podMaxBackoffSeconds = podMaxBackoffSeconds
	}
}

// WithExtenders sets extenders for the Scheduler
func WithExtenders(e ...schedulerapi.Extender) Option {
	return func(o *schedulerOptions) {
		o.extenders = e
	}
}

var defaultSchedulerOptions = schedulerOptions{
	profiles: []schedulerapi.KubeSchedulerProfile{
		// Profiles' default plugins are set from the algorithm provider.
		{SchedulerName: v1.DefaultSchedulerName},
	},
	schedulerAlgorithmSource: schedulerapi.SchedulerAlgorithmSource{
		Provider: defaultAlgorithmSourceProviderName(),
	},
	disablePreemption:              false,
	percentageOfNodesToScore:       schedulerapi.DefaultPercentageOfNodesToScore,
	bindTimeoutSeconds:             BindTimeoutSeconds,
	podInitialBackoffSeconds:       int64(internalqueue.DefaultPodInitialBackoffDuration.Seconds()),
	podMaxBackoffSeconds:           int64(internalqueue.DefaultPodMaxBackoffDuration.Seconds()),
	nodePackageResourceMatchFactor: schedulerapi.DefaultNodePackageFactor,
}

// New returns a Scheduler
func New(client clientset.Interface,
	informerFactory informers.SharedInformerFactory,
	bytedinformerFactory bytedinformers.SharedInformerFactory,
	podInformer coreinformers.PodInformer,
	recorderFactory profile.RecorderFactory,
	stopCh <-chan struct{},
	opts ...Option) (*Scheduler, error) {

	stopEverything := stopCh
	if stopEverything == nil {
		stopEverything = wait.NeverStop
	}

	options := defaultSchedulerOptions
	for _, opt := range opts {
		opt(&options)
	}

	schedulerCache := internalcache.New(30*time.Second, stopEverything)
	volumeBinder := scheduling.NewVolumeBinder(
		client,
		informerFactory.Core().V1().Nodes(),
		informerFactory.Storage().V1().CSINodes(),
		informerFactory.Core().V1().PersistentVolumeClaims(),
		informerFactory.Core().V1().PersistentVolumes(),
		informerFactory.Storage().V1().StorageClasses(),
		time.Duration(options.bindTimeoutSeconds)*time.Second,
	)

	registry := frameworkplugins.NewInTreeRegistry()
	if err := registry.Merge(options.frameworkOutOfTreeRegistry); err != nil {
		return nil, err
	}

	snapshot := internalcache.NewEmptySnapshot()

	configurator := &Configurator{
		client:                         client,
		bytedinformerFactory:           bytedinformerFactory,
		recorderFactory:                recorderFactory,
		informerFactory:                informerFactory,
		podInformer:                    podInformer,
		volumeBinder:                   volumeBinder,
		schedulerCache:                 schedulerCache,
		StopEverything:                 stopEverything,
		disablePreemption:              options.disablePreemption,
		percentageOfNodesToScore:       options.percentageOfNodesToScore,
		bindTimeoutSeconds:             options.bindTimeoutSeconds,
		podInitialBackoffSeconds:       options.podInitialBackoffSeconds,
		podMaxBackoffSeconds:           options.podMaxBackoffSeconds,
		enableNonPreempting:            utilfeature.DefaultFeatureGate.Enabled(kubefeatures.NonPreemptingPriority),
		profiles:                       append([]schedulerapi.KubeSchedulerProfile(nil), options.profiles...),
		registry:                       registry,
		nodeInfoSnapshot:               snapshot,
		extenders:                      options.extenders,
		nodePackageResourceMatchFactor: options.nodePackageResourceMatchFactor,
	}

	metrics.Register()

	var sched *Scheduler
	source := options.schedulerAlgorithmSource
	switch {
	case source.Provider != nil:
		// Create the config from a named algorithm provider.
		sc, err := configurator.createFromProvider(*source.Provider)
		if err != nil {
			return nil, fmt.Errorf("couldn't create scheduler using provider %q: %v", *source.Provider, err)
		}
		sched = sc
	case source.Policy != nil:
		// Create the config from a user specified policy source.
		policy := &schedulerapi.Policy{}
		switch {
		case source.Policy.File != nil:
			if err := initPolicyFromFile(source.Policy.File.Path, policy); err != nil {
				return nil, err
			}
		case source.Policy.ConfigMap != nil:
			if err := initPolicyFromConfigMap(client, source.Policy.ConfigMap, policy); err != nil {
				return nil, err
			}
		}
		// Set extenders on the configurator now that we've decoded the policy
		// In this case, c.extenders should be nil since we're using a policy (and therefore not componentconfig,
		// which would have set extenders in the above instantiation of Configurator from CC options)
		configurator.extenders = policy.Extenders
		sc, err := configurator.createFromConfig(*policy)
		if err != nil {
			return nil, fmt.Errorf("couldn't create scheduler from policy: %v", err)
		}
		sched = sc
	default:
		return nil, fmt.Errorf("unsupported algorithm source: %v", source)
	}
	// Additional tweaks to the config produced by the configurator.
	sched.DisablePreemption = options.disablePreemption
	sched.StopEverything = stopEverything
	sched.podConditionUpdater = &podConditionUpdaterImpl{client}
	sched.podPreemptor = &podPreemptorImpl{client}
	sched.scheduledPodsHasSynced = podInformer.Informer().HasSynced
	sched.podUpdater = &podUpdaterImpl{client}

	addAllEventHandlers(sched, informerFactory, podInformer, bytedinformerFactory)
	return sched, nil
}

// initPolicyFromFile initialize policy from file
func initPolicyFromFile(policyFile string, policy *schedulerapi.Policy) error {
	// Use a policy serialized in a file.
	_, err := os.Stat(policyFile)
	if err != nil {
		return fmt.Errorf("missing policy config file %s", policyFile)
	}
	data, err := ioutil.ReadFile(policyFile)
	if err != nil {
		return fmt.Errorf("couldn't read policy config: %v", err)
	}
	err = runtime.DecodeInto(scheme.Codecs.UniversalDecoder(), []byte(data), policy)
	if err != nil {
		return fmt.Errorf("invalid policy: %v", err)
	}
	return nil
}

// initPolicyFromConfigMap initialize policy from configMap
func initPolicyFromConfigMap(client clientset.Interface, policyRef *schedulerapi.SchedulerPolicyConfigMapSource, policy *schedulerapi.Policy) error {
	// Use a policy serialized in a config map value.
	policyConfigMap, err := client.CoreV1().ConfigMaps(policyRef.Namespace).Get(context.TODO(), policyRef.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("couldn't get policy config map %s/%s: %v", policyRef.Namespace, policyRef.Name, err)
	}
	data, found := policyConfigMap.Data[schedulerapi.SchedulerPolicyConfigMapKey]
	if !found {
		return fmt.Errorf("missing policy config map value at key %q", schedulerapi.SchedulerPolicyConfigMapKey)
	}
	err = runtime.DecodeInto(scheme.Codecs.UniversalDecoder(), []byte(data), policy)
	if err != nil {
		return fmt.Errorf("invalid policy: %v", err)
	}
	return nil
}

func (sched *Scheduler) AddCPUMemoryResource(pod *v1.Pod, dest string) (error, bool) {
	var shouldUpdate bool
	node := sched.SchedulerCache.GetNodeInfo(dest).Node()
	if node == nil {
		return fmt.Errorf("selected node is missing in cache: %s", dest), shouldUpdate
	}
	nodeCap := node.Status.Capacity
	nodeAlloc := node.Status.Allocatable
	socketCap := nodeCap[v1.ResourceBytedanceSocket]
	socketAlloc := nodeAlloc[v1.ResourceBytedanceSocket]
	cpuCap := nodeCap[v1.ResourceCPU]
	cpuAlloc := nodeAlloc[v1.ResourceCPU]
	memCap := nodeCap[v1.ResourceMemory]
	memAlloc := nodeAlloc[v1.ResourceMemory]
	for i, container := range pod.Spec.Containers {
		socketReq := container.Resources.Requests[v1.ResourceBytedanceSocket]
		if socketReq.Value() == 0 {
			continue
		}
		if socketCap.Value() == 0 || socketAlloc.Value() == 0 {
			return fmt.Errorf("the socket capacity or allocatable is 0 in node %s", dest), shouldUpdate
		}
		cpuReq := container.Resources.Requests[v1.ResourceCPU]
		if cpuReq.Value() == 0 {
			// Every numa minus cpu reserved in each numa and minus extra one cpu in each numa
			cpuReqVal := cpuAlloc.Value()*socketReq.Value()/socketAlloc.Value() - socketReq.Value()
			pod.Spec.Containers[i].Resources.Requests[v1.ResourceCPU] = *resource.NewQuantity(cpuReqVal, resource.DecimalSI)
			shouldUpdate = true
		}
		cpuLmt := container.Resources.Limits[v1.ResourceCPU]
		if cpuLmt.Value() == 0 {
			cpuLmtVal := cpuCap.Value() * socketReq.Value() / socketCap.Value()
			pod.Spec.Containers[i].Resources.Limits[v1.ResourceCPU] = *resource.NewQuantity(cpuLmtVal, resource.DecimalSI)
			shouldUpdate = true
		}
		memReq := container.Resources.Requests[v1.ResourceMemory]
		if memReq.Value() == 0 {
			// Every numa minua memory reserved in each numa and minus extra 1 Gi in each numa
			extraMemReserved := resource.NewQuantity(1*1024*1024*1024, resource.BinarySI)
			memReqVal := memAlloc.Value()*socketReq.Value()/socketAlloc.Value() - socketReq.Value()*extraMemReserved.Value()
			pod.Spec.Containers[i].Resources.Requests[v1.ResourceMemory] = *resource.NewQuantity(memReqVal, resource.BinarySI)
			shouldUpdate = true
		}
		memLmt := container.Resources.Limits[v1.ResourceMemory]
		if memLmt.Value() == 0 {
			memLmtVal := memCap.Value() * socketReq.Value() / socketCap.Value()
			pod.Spec.Containers[i].Resources.Limits[v1.ResourceMemory] = *resource.NewQuantity(memLmtVal, resource.BinarySI)
			shouldUpdate = true
		}
	}

	return nil, shouldUpdate
}

// Run begins watching and scheduling. It waits for cache to be synced, then starts scheduling and blocked until the context is done.
func (sched *Scheduler) Run(ctx context.Context) {
	if !cache.WaitForCacheSync(ctx.Done(), sched.scheduledPodsHasSynced) {
		return
	}
	sched.SchedulingQueue.Run()
	wait.UntilWithContext(ctx, sched.scheduleOne, 0)
	sched.SchedulingQueue.Close()
}

// recordFailedSchedulingEvent records an event for the pod that indicates the
// pod has failed to schedule.
// NOTE: This function modifies "pod". "pod" should be copied before being passed.
func (sched *Scheduler) recordSchedulingFailure(prof *profile.Profile, podInfo *framework.PodInfo, err error, reason string, message string) {
	sched.Error(podInfo, err)
	metrics.SchedulingFailedCounter.Inc()
	pod := podInfo.Pod
	prof.Recorder.Eventf(pod, nil, v1.EventTypeWarning, "FailedScheduling", "Scheduling", message)
	if err := sched.podConditionUpdater.update(pod, &v1.PodCondition{
		Type:    v1.PodScheduled,
		Status:  v1.ConditionFalse,
		Reason:  reason,
		Message: err.Error(),
	}); err != nil {
		klog.Errorf("Error updating the condition of the pod %s/%s: %v", pod.Namespace, pod.Name, err)
	}
}

func podHasLeastPriority(pod *v1.Pod) bool {
	if pod.Spec.Priority == nil || *pod.Spec.Priority == 0 {
		// we don't have a customized PriorityClass whose priority value is 0
		// so we reach here only when the pod's priority is not set
		return true
	}

	/*
		if pod.Spec.Priority != nil {
			if *pod.Spec.Priority == 0 && !util.HasResource(pod, util.ResourceGPU) {
				return true
			}
		}
	*/

	return false
}

// preempt tries to create room for a pod that has failed to schedule, by preempting lower priority pods if possible.
// If it succeeds, it adds the name of the node where preemption has happened to the pod spec.
// It returns the node name and an error if any.
func (sched *Scheduler) preempt(ctx context.Context, prof *profile.Profile, state *framework.CycleState, preemptor *v1.Pod, scheduleErr error) (string, error) {
	if podHasLeastPriority(preemptor) {
		klog.V(3).Infof("Pod has least priority or priority is not set")
		return "", nil
	}

	preemptor, err := sched.podPreemptor.getUpdatedPod(preemptor)
	if err != nil {
		klog.Errorf("Error getting the updated preemptor pod object: %v", err)
		return "", err
	}

	// TODO: revisit this to see if it is ok to return directly here
	// we will never remove NominatedNodeName in pod status if it is not cached
	if len(preemptor.Status.NominatedNodeName) > 0 || len(preemptor.Spec.NodeName) > 0 {
		// if the nominated node is set, return directly
		return "", nil
	}

	node, victims, nominatedPodsToClear, err := sched.Algorithm.Preempt(ctx, prof, state, preemptor, scheduleErr)

	if err != nil {
		klog.Errorf("Error preempting victims to make room for %v/%v: %v", preemptor.Namespace, preemptor.Name, err)
		return "", err
	}
	var nodeName = ""
	if node != nil {
		klog.V(6).Infof("pods to be preempted is on node: %v", node.Name)
		klog.V(6).Infof("victims number is: %d", len(victims))
		klog.V(6).Infof("nominatedPods number to be cleared is: %d", len(nominatedPodsToClear))

		nodeName = node.Name
		// Update the scheduling queue with the nominated pod information. Without
		// this, there would be a race condition between the next scheduling cycle
		// and the time the scheduler receives a Pod Update for the nominated pod.
		sched.SchedulingQueue.UpdateNominatedPodForNode(preemptor, nodeName)

		// Make a call to update nominated node name of the pod on the API server.
		err = sched.podPreemptor.setNominatedNodeName(preemptor, nodeName)
		if err != nil {
			klog.Errorf("Error in preemption process. Cannot set 'NominatedPod' on pod %v/%v: %v", preemptor.Namespace, preemptor.Name, err)
			sched.SchedulingQueue.DeleteNominatedPodIfExists(preemptor)
			return "", err
		}

		// cache preemptor in scheduler cache
		preemptorCopy := preemptor.DeepCopy()
		preemptorCopy.Status.NominatedNodeName = nodeName
		sched.SchedulerCache.CachePreemptor(preemptorCopy)

		for _, victim := range victims {
			if err = sched.podPreemptor.deletePod(victim); err != nil {
				klog.Errorf("Error preempting pod %v/%v: %v", victim.Namespace, victim.Name, err)
				return "", err
			}
			// If the victim is a WaitingPod, send a reject message to the PermitPlugin
			if waitingPod := prof.GetWaitingPod(victim.UID); waitingPod != nil {
				waitingPod.Reject("preempted")
			}
			prof.Recorder.Eventf(victim, preemptor, v1.EventTypeNormal, "Preempted", "Preempting", "Preempted by %v/%v on node %v", preemptor.Namespace, preemptor.Name, nodeName)

			// cache victims for deployment
			deployName := util.GetDeployNameFromPod(victim)
			if len(deployName) > 0 {
				sched.SchedulerCache.AddOneVictim(deployName, string(victim.UID))
			}
		}
		metrics.PreemptionVictims.Observe(float64(len(victims)))
	} else {
		klog.V(6).Infof("the node selected by Preempt function is nil")
	}
	// Clearing nominated pods should happen outside of "if node != nil". Node could
	// be nil when a pod with nominated node name is eligible to preempt again,
	// but preemption logic does not find any node for it. In that case Preempt()
	// function of generic_scheduler.go returns the pod itself for removal of
	// the 'NominatedPod' field.
	for _, p := range nominatedPodsToClear {
		rErr := sched.podPreemptor.removeNominatedNodeName(p)
		if rErr != nil {
			klog.Errorf("Cannot remove 'NominatedPod' field of pod: %v", rErr)
			// We do not return as this error is not critical.
		}
	}
	return nodeName, err
}

// bindVolumes will make the API update with the assumed bindings and wait until
// the PV controller has completely finished the binding operation.
//
// If binding errors, times out or gets undone, then an error will be returned to
// retry scheduling.
func (sched *Scheduler) bindVolumes(assumed *v1.Pod) error {
	bindingVolumesStart := time.Now()
	klog.V(5).Infof("Trying to bind volumes for pod \"%v/%v\"", assumed.Namespace, assumed.Name)
	err := sched.VolumeBinder.BindPodVolumes(assumed)
	if err != nil {
		klog.V(1).Infof("Failed to bind volumes for pod \"%v/%v\": %v", assumed.Namespace, assumed.Name, err)

		// Unassume the Pod and retry scheduling
		if forgetErr := sched.SchedulerCache.ForgetPod(assumed); forgetErr != nil {
			klog.Errorf("scheduler cache ForgetPod failed: %v", forgetErr)
		}

		return err
	}

	metrics.BindingVolumesLatency.Observe(metrics.SinceInSeconds(bindingVolumesStart))

	klog.V(5).Infof("Success binding volumes for pod \"%v/%v\"", assumed.Namespace, assumed.Name)
	return nil
}

// assume signals to the cache that a pod is already in the cache, so that binding can be asynchronous.
// assume modifies `assumed`.
func (sched *Scheduler) assume(assumed *v1.Pod, host string) error {
	// Optimistically assume that the binding will succeed and send it to apiserver
	// in the background.
	// If the binding fails, scheduler will release resources allocated to assumed pod
	// immediately.
	assumed.Spec.NodeName = host

	if err := sched.SchedulerCache.AssumePod(assumed); err != nil {
		klog.Errorf("scheduler cache AssumePod failed: %v", err)
		return err
	}
	// if "assumed" is a nominated pod, we should remove it from internal cache
	if sched.SchedulingQueue != nil {
		sched.SchedulingQueue.DeleteNominatedPodIfExists(assumed)
	}

	return nil
}

// bind binds a pod to a given node defined in a binding object.
// The precedence for binding is: (1) extenders and (2) framework plugins.
// We expect this to run asynchronously, so we handle binding metrics internally.
func (sched *Scheduler) bind(ctx context.Context, prof *profile.Profile, assumed *v1.Pod, targetNode string, state *framework.CycleState) (err error) {
	start := time.Now()
	defer func() {
		sched.finishBinding(prof, assumed, targetNode, start, err)
	}()

	bound, err := sched.extendersBinding(assumed, targetNode)
	if bound {
		return err
	}
	bindStatus := prof.RunBindPlugins(ctx, state, assumed, targetNode)
	if bindStatus.IsSuccess() {
		return nil
	}
	if bindStatus.Code() == framework.Error {
		return bindStatus.AsError()
	}
	return fmt.Errorf("bind status: %s, %v", bindStatus.Code().String(), bindStatus.Message())
}

// TODO(#87159): Move this to a Plugin.
func (sched *Scheduler) extendersBinding(pod *v1.Pod, node string) (bool, error) {
	for _, extender := range sched.Algorithm.Extenders() {
		if !extender.IsBinder() || !extender.IsInterested(pod) {
			continue
		}
		return true, extender.Bind(&v1.Binding{
			ObjectMeta: metav1.ObjectMeta{Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID},
			Target:     v1.ObjectReference{Kind: "Node", Name: node},
		})
	}
	return false, nil
}

func (sched *Scheduler) finishBinding(prof *profile.Profile, assumed *v1.Pod, targetNode string, start time.Time, err error) {
	if finErr := sched.SchedulerCache.FinishBinding(assumed); finErr != nil {
		klog.Errorf("scheduler cache FinishBinding failed: %v", finErr)
	}
	if err != nil {
		klog.V(1).Infof("Failed to bind pod: %v/%v", assumed.Namespace, assumed.Name)
		if err := sched.SchedulerCache.ForgetPod(assumed); err != nil {
			klog.Errorf("scheduler cache ForgetPod failed: %v", err)
		}
		return
	}

	metrics.BindingLatency.Observe(metrics.SinceInSeconds(start))
	metrics.DeprecatedSchedulingDuration.WithLabelValues(metrics.Binding).Observe(metrics.SinceInSeconds(start))
	prof.Recorder.Eventf(assumed, nil, v1.EventTypeNormal, "Scheduled", "Binding", "Successfully assigned %v/%v to %v", assumed.Namespace, assumed.Name, targetNode)
	if len(assumed.Status.NominatedNodeName) > 0 {
		preemptionMessage := "pod is scheduled successfully because of preemption"
		prof.Recorder.Eventf(assumed, nil, v1.EventTypeNormal, "ScheduledDueToPreemption", "%v", preemptionMessage)
	}
}

// scheduleOne does the entire scheduling workflow for a single pod.  It is serialized on the scheduling algorithm's host fitting.
func (sched *Scheduler) scheduleOne(ctx context.Context) {
	podInfo := sched.NextPod()
	// pod could be nil when schedulerQueue is closed
	if podInfo == nil || podInfo.Pod == nil {
		return
	}
	pod := podInfo.Pod
	prof, err := sched.profileForPod(pod)
	if err != nil {
		// This shouldn't happen, because we only accept for scheduling the pods
		// which specify a scheduler name that matches one of the profiles.
		klog.Error(err)
		return
	}
	if sched.skipPodSchedule(prof, pod) {
		return
	}

	if pod.Annotations != nil && len(pod.Annotations[util.SocketToCpuKey]) > 0 {
		klog.V(3).Infof("Pod: %v/%v is already bound because the sockettocpu annotation is set, skip scheduling", pod.Namespace, pod.Name)
		return
	}
	if len(pod.Spec.NodeName) > 0 {
		klog.V(3).Infof("Pod: %v/%v is already bound, skip scheduling", pod.Namespace, pod.Name)
		return
	}

	klog.V(3).Infof("Attempting to schedule pod: %v/%v", pod.Namespace, pod.Name)

	// Synchronously attempt to find a fit for the pod.
	start := time.Now()
	state := framework.NewCycleState()
	state.SetRecordPluginMetrics(rand.Intn(100) < pluginMetricsSamplePercent)
	schedulingCycleCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	scheduleResult, err := sched.Algorithm.Schedule(schedulingCycleCtx, prof, state, pod)
	if err != nil {
		// Schedule() may have failed because the pod would not fit on any host, so we try to
		// preempt, with the expectation that the next time the pod is tried for scheduling it
		// will fit due to the preemption. It is also possible that a different pod will schedule
		// into the resources that were preempted, but this is harmless.
		if fitError, ok := err.(*core.FitError); ok {
			// add this for online debug
			if pod.Annotations != nil && len(pod.Annotations[util.PodDebugModeAnnotationKey]) > 0 {
				klog.Infof("pod is in debug mode, print detailed predicate result")
				klog.Error(fitError)
			}

			if sched.DisablePreemption {
				klog.V(3).Infof("Pod priority feature is not enabled or preemption is disabled by scheduler configuration." +
					" No preemption is performed.")
			} else {
				// if pod is victim, do not perform preemption for it, let SRE platform add machines directly
				/*deployName := util.GetDeployNameFromPod(pod)
				if len(deployName) > 0 && sched.config.SchedulerCache.IsVictims(deployName) {
					// pod is victim, do not perform preemption for it
					// send out event and let SRE platform add machines

					// TODO: need to revisit this later
					// the pod sending out the SRE event may be placed to existing machine(there may be some pods being killed),
					// the machine added by SRE platform may be consumed by other pods
					// do we need to send out event continuously ?
					// if so, what action should SRE platform take ?
					// SRE may add machines when having gathered 10 events ?
					podC := pod.DeepCopy()
					message := "SRE Platform Notice: victim: " + podC.Name + ", deployment name: " + deployName + ", can not be scheduled, please add machines for it. "
					message = message + "Refined resources requirement: " + util.PodRefinedResourceRequestToString(podC.Annotations)
					sched.config.Recorder.Event(podC, v1.EventTypeWarning, "FailedScheduling", message)

					// Pod did not fit anywhere, so it is counted as a failure.
					metrics.PodScheduleFailures.Inc()

					return
				}*/

				preemptionStartTime := time.Now()
				victimNodeName, preemptErr := sched.preempt(schedulingCycleCtx, prof, state, pod, fitError)
				if preemptErr == nil && len(victimNodeName) > 0 {
					metrics.PreemptionSuccessCounter.Inc()
				} else {
					podC := pod.DeepCopy()
					prof.Recorder.Eventf(podC, nil, v1.EventTypeWarning, "FailedPreempting", "Preempting failed for pod", "")
				}
				metrics.PreemptionAttempts.Inc()
				metrics.SchedulingAlgorithmPreemptionEvaluationDuration.Observe(metrics.SinceInSeconds(preemptionStartTime))
				metrics.DeprecatedSchedulingDuration.WithLabelValues(metrics.PreemptionEvaluation).Observe(metrics.SinceInSeconds(preemptionStartTime))
			}
			// Pod did not fit anywhere, so it is counted as a failure. If preemption
			// succeeds, the pod should get counted as a success the next time we try to
			// schedule it. (hopefully)
			metrics.PodScheduleFailures.Inc()
		} else if err == core.ErrNoNodesAvailable {
			// No nodes available is counted as unschedulable rather than an error.
			metrics.PodScheduleFailures.Inc()
		} else {
			klog.Errorf("error selecting node for pod: %v", err)
			metrics.PodScheduleErrors.Inc()

			if len(pod.Status.NominatedNodeName) > 0 {
				// hasChanceBeforeReduceOne := sched.config.SchedulerCache.PreemptorStillHaveChance(pod)
				// pod is preemptor and it is not scheduled successfully
				sched.SchedulerCache.ReduceOneChanceForPreemptor(pod)
				if !sched.SchedulerCache.PreemptorStillHaveChance(pod) /*&& hasChanceBeforeReduceOne */ {
					sched.podPreemptor.removeNominatedNodeName(pod)
				}
			}
		}
		sched.recordSchedulingFailure(prof, podInfo.DeepCopy(), err, v1.PodReasonUnschedulable, err.Error())
		return
	}

	if len(pod.Status.NominatedNodeName) > 0 {
		// pod is preemptor and it is scheduled successfully
		// remove the cache info
		sched.SchedulerCache.DeletePreemptorFromCacheOnly(pod)
	}

	/*deployName := util.GetDeployNameFromPod(pod)
	if len(deployName) > 0 && sched.config.SchedulerCache.IsVictims(deployName) {
		// pod is victim, and it is scheduled successfully, subtract it from cache
		sched.config.SchedulerCache.SubtractOneVictim(deployName)
	}*/

	metrics.SchedulingAlgorithmLatency.Observe(metrics.SinceInSeconds(start))

	algorithmEndsTime := time.Now()

	podToUpdate := pod.DeepCopy()
	addResourceErr, shouldUpdate := sched.AddCPUMemoryResource(podToUpdate, scheduleResult.SuggestedHost)
	if addResourceErr != nil {
		klog.Errorf("Failed to schedule: %v, fail to add cpu/memory resource for socket policy.", podToUpdate)
		sched.Error(podInfo.DeepCopy(), addResourceErr)
		prof.Recorder.Eventf(podToUpdate, nil, v1.EventTypeWarning, "FailedScheduling", "%v", addResourceErr.Error())
		sched.podConditionUpdater.update(podToUpdate, &v1.PodCondition{
			Type:   v1.PodScheduled,
			Status: v1.ConditionFalse,
			Reason: "Unschedulable",
		})
		return
	}
	if podToUpdate.Annotations == nil {
		podToUpdate.Annotations = make(map[string]string)
	}
	podToUpdate.Annotations[util.SocketToCpuKey] = "done"

	// if share gpu feature gate is enabled, allocate physical gpu and update pod annotation
	if utilfeature.DefaultFeatureGate.Enabled(features.ShareGPU) && util.IsGPUSharingPod(podToUpdate) {
		shouldUpdate = true
		nodeInfo := sched.Cache().GetNodeInfo(scheduleResult.SuggestedHost)
		if nodeInfo == nil {
			klog.Errorf("Failed to schedule: %v, node:%s isn't in cache any more.", podToUpdate, scheduleResult.SuggestedHost)
			sched.Error(podInfo.DeepCopy(), fmt.Errorf("node:%s isn't in cache any more", scheduleResult.SuggestedHost))
			prof.Recorder.Eventf(podToUpdate, nil, v1.EventTypeWarning, "FailedScheduling", "%v", " suggest host isn't in cache any more")
			sched.podConditionUpdater.update(podToUpdate, &v1.PodCondition{
				Type:   v1.PodScheduled,
				Status: v1.ConditionFalse,
				Reason: "Unschedulable",
			})
			return
		}
		allocateShareGPUErr := nodeInfo.NodeShareGPUDeviceInfo.AllocateShareGPU(podToUpdate)
		if allocateShareGPUErr != nil {
			klog.Errorf("Failed to schedule: %v, fail to allocate physical gpu.", podToUpdate)
			sched.Error(podInfo.DeepCopy(), allocateShareGPUErr)
			prof.Recorder.Eventf(podToUpdate, nil, v1.EventTypeWarning, "FailedScheduling", "%v", allocateShareGPUErr.Error())
			sched.podConditionUpdater.update(podToUpdate, &v1.PodCondition{
				Type:   v1.PodScheduled,
				Status: v1.ConditionFalse,
				Reason: "Unschedulable",
			})
			return
		}
	}

	// Tell the cache to assume that a pod now is running on a given node, even though it hasn't been bound yet.
	// This allows us to keep scheduling without waiting on binding to occur.
	assumedPodInfo := podInfo.DeepCopy()
	assumedPod := assumedPodInfo.Pod

	assumedPodCPUAddErr, _ := sched.AddCPUMemoryResource(assumedPod, scheduleResult.SuggestedHost)
	if assumedPodCPUAddErr != nil {
		klog.Errorf("Failed to schedule: %v, fail to add cpu resource for socket policy.", assumedPod)
		sched.Error(assumedPodInfo, assumedPodCPUAddErr)
		prof.Recorder.Eventf(assumedPod, nil, v1.EventTypeWarning, "FailedScheduling", "%v", assumedPodCPUAddErr.Error())
		sched.podConditionUpdater.update(assumedPod, &v1.PodCondition{
			Type:   v1.PodScheduled,
			Status: v1.ConditionFalse,
			Reason: "Unschedulable",
		})
		return
	}

	// Assume volumes first before assuming the pod.
	//
	// If all volumes are completely bound, then allBound is true and binding will be skipped.
	//
	// Otherwise, binding of volumes is started after the pod is assumed, but before pod binding.
	//
	// This function modifies 'assumedPod' if volume binding is required.
	allBound, err := sched.VolumeBinder.AssumePodVolumes(assumedPod, scheduleResult.SuggestedHost)
	if err != nil {
		sched.recordSchedulingFailure(prof, assumedPodInfo, err, SchedulerError,
			fmt.Sprintf("AssumePodVolumes failed: %v", err))
		metrics.PodScheduleErrors.Inc()
		return
	}

	// Run "reserve" plugins.
	if sts := prof.RunReservePlugins(schedulingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost); !sts.IsSuccess() {
		sched.recordSchedulingFailure(prof, assumedPodInfo, sts.AsError(), SchedulerError, sts.Message())
		metrics.PodScheduleErrors.Inc()
		return
	}

	// assume modifies `assumedPod` by setting NodeName=scheduleResult.SuggestedHost
	err = sched.assume(assumedPod, scheduleResult.SuggestedHost)
	if err != nil {
		// This is most probably result of a BUG in retrying logic.
		// We report an error here so that pod scheduling can be retried.
		// This relies on the fact that Error will check if the pod has been bound
		// to a node and if so will not add it back to the unscheduled pods queue
		// (otherwise this would cause an infinite loop).
		sched.recordSchedulingFailure(prof, assumedPodInfo, err, SchedulerError, fmt.Sprintf("AssumePod failed: %v", err))
		metrics.PodScheduleErrors.Inc()
		// trigger un-reserve plugins to clean up state associated with the reserved Pod
		prof.RunUnreservePlugins(schedulingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost)
		return
	}

	// Run "permit" plugins.
	runPermitStatus := prof.RunPermitPlugins(schedulingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost)
	if runPermitStatus.Code() != framework.Wait && !runPermitStatus.IsSuccess() {
		var reason string
		if runPermitStatus.IsUnschedulable() {
			metrics.PodScheduleFailures.Inc()
			reason = v1.PodReasonUnschedulable
		} else {
			metrics.PodScheduleErrors.Inc()
			reason = SchedulerError
		}
		if forgetErr := sched.Cache().ForgetPod(assumedPod); forgetErr != nil {
			klog.Errorf("scheduler cache ForgetPod failed: %v", forgetErr)
		}
		// One of the plugins returned status different than success or wait.
		prof.RunUnreservePlugins(schedulingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost)
		sched.recordSchedulingFailure(prof, assumedPodInfo, runPermitStatus.AsError(), reason, runPermitStatus.Message())
		return
	}

	metrics.AssumingLatency.Observe(metrics.SinceInSeconds(algorithmEndsTime))
	mainProcessEndsTime := time.Now()

	// bind the pod to its host asynchronously (we can do this b/c of the assumption step above).
	go func() {
		bindingCycleCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		metrics.SchedulerGoroutines.WithLabelValues("binding").Inc()
		defer metrics.SchedulerGoroutines.WithLabelValues("binding").Dec()

		waitOnPermitStatus := prof.WaitOnPermit(bindingCycleCtx, assumedPod)
		if !waitOnPermitStatus.IsSuccess() {
			var reason string
			if waitOnPermitStatus.IsUnschedulable() {
				metrics.PodScheduleFailures.Inc()
				reason = v1.PodReasonUnschedulable
			} else {
				metrics.PodScheduleErrors.Inc()
				reason = SchedulerError
			}
			if forgetErr := sched.Cache().ForgetPod(assumedPod); forgetErr != nil {
				klog.Errorf("scheduler cache ForgetPod failed: %v", forgetErr)
			}
			// trigger un-reserve plugins to clean up state associated with the reserved Pod
			prof.RunUnreservePlugins(bindingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost)
			sched.recordSchedulingFailure(prof, assumedPodInfo, waitOnPermitStatus.AsError(), reason, waitOnPermitStatus.Message())
			return
		}

		metrics.AlgorithmToBindingLatency.Observe(metrics.SinceInSeconds(mainProcessEndsTime))
		if shouldUpdate {
			klog.V(4).Infof("Starting updating cpu/memory resource for the pod: %s", podToUpdate.Name)
			errUpdate := sched.podUpdater.update(podToUpdate)
			if errUpdate != nil {
				klog.Errorf("Failed to Update pod: %v/%v %v", podToUpdate.Namespace, podToUpdate.Name, errUpdate)
				if err := sched.SchedulerCache.ForgetPod(assumedPod); err != nil {
					klog.Errorf("scheduler cache ForgetPod failed 1: %v", err)
				}
				sched.Error(podInfo.DeepCopy(), errUpdate)
				prof.Recorder.Eventf(podToUpdate, nil, v1.EventTypeNormal, "FailedScheduling", "Update failed: %v", errUpdate.Error())
				sched.podConditionUpdater.update(podToUpdate, &v1.PodCondition{
					Type:   v1.PodScheduled,
					Status: v1.ConditionFalse,
					Reason: "BindingRejected",
				})
				return
			}
		}

		// Bind volumes first before Pod
		if !allBound {
			err := sched.bindVolumes(assumedPod)
			if err != nil {
				sched.recordSchedulingFailure(prof, assumedPodInfo, err, "VolumeBindingFailed", err.Error())
				metrics.PodScheduleErrors.Inc()
				// trigger un-reserve plugins to clean up state associated with the reserved Pod
				prof.RunUnreservePlugins(bindingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost)
				return
			}
		}

		// Run "prebind" plugins.
		preBindStatus := prof.RunPreBindPlugins(bindingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost)
		if !preBindStatus.IsSuccess() {
			var reason string
			metrics.PodScheduleErrors.Inc()
			reason = SchedulerError
			if forgetErr := sched.Cache().ForgetPod(assumedPod); forgetErr != nil {
				klog.Errorf("scheduler cache ForgetPod failed: %v", forgetErr)
			}
			// trigger un-reserve plugins to clean up state associated with the reserved Pod
			prof.RunUnreservePlugins(bindingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost)
			sched.recordSchedulingFailure(prof, assumedPodInfo, preBindStatus.AsError(), reason, preBindStatus.Message())
			return
		}

		err := sched.bind(bindingCycleCtx, prof, assumedPod, scheduleResult.SuggestedHost, state)
		metrics.E2eSchedulingLatency.Observe(metrics.SinceInSeconds(start))
		if err != nil {
			metrics.PodScheduleErrors.Inc()
			// trigger un-reserve plugins to clean up state associated with the reserved Pod
			prof.RunUnreservePlugins(bindingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost)
			sched.recordSchedulingFailure(prof, assumedPodInfo, err, SchedulerError, fmt.Sprintf("Binding rejected: %v", err))
		} else {
			// Calculating nodeResourceString can be heavy. Avoid it if klog verbosity is below 2.
			if klog.V(2) {
				klog.Infof("pod %v/%v is bound successfully on node %q, %d nodes evaluated, %d nodes were found feasible.", assumedPod.Namespace, assumedPod.Name, scheduleResult.SuggestedHost, scheduleResult.EvaluatedNodes, scheduleResult.FeasibleNodes)
			}

			metrics.PodScheduleSuccesses.Inc()
			metrics.PodSchedulingAttempts.Observe(float64(podInfo.Attempts))
			metrics.PodSchedulingDuration.Observe(metrics.SinceInSeconds(podInfo.InitialAttemptTimestamp))

			// Run "postbind" plugins.
			prof.RunPostBindPlugins(bindingCycleCtx, state, assumedPod, scheduleResult.SuggestedHost)
		}
	}()
}

func (sched *Scheduler) profileForPod(pod *v1.Pod) (*profile.Profile, error) {
	prof, ok := sched.Profiles[pod.Spec.SchedulerName]
	if !ok {
		return nil, fmt.Errorf("profile not found for scheduler name %q", pod.Spec.SchedulerName)
	}
	return prof, nil
}

// skipPodSchedule returns true if we could skip scheduling the pod for specified cases.
func (sched *Scheduler) skipPodSchedule(prof *profile.Profile, pod *v1.Pod) bool {
	// Case 1: pod is being deleted.
	if pod.DeletionTimestamp != nil {
		prof.Recorder.Eventf(pod, nil, v1.EventTypeWarning, "FailedScheduling", "Scheduling", "skip schedule deleting pod: %v/%v", pod.Namespace, pod.Name)
		klog.V(3).Infof("Skip schedule deleting pod: %v/%v", pod.Namespace, pod.Name)
		return true
	}

	// Case 2: pod has been assumed and pod updates could be skipped.
	// An assumed pod can be added again to the scheduling queue if it got an update event
	// during its previous scheduling cycle but before getting assumed.
	if sched.skipPodUpdate(pod) {
		return true
	}

	return false
}

type podConditionUpdaterImpl struct {
	Client clientset.Interface
}

func (p *podConditionUpdaterImpl) update(pod *v1.Pod, condition *v1.PodCondition) error {
	klog.V(3).Infof("Updating pod condition for %s/%s to (%s==%s, Reason=%s)", pod.Namespace, pod.Name, condition.Type, condition.Status, condition.Reason)
	if podutil.UpdatePodCondition(&pod.Status, condition) {
		_, err := p.Client.CoreV1().Pods(pod.Namespace).UpdateStatus(context.TODO(), pod, metav1.UpdateOptions{})
		return err
	}
	return nil
}

type podUpdaterImpl struct {
	Client clientset.Interface
}

func (p *podUpdaterImpl) update(pod *v1.Pod) error {
	klog.V(2).Infof("Updating pod %s/%s", pod.Namespace, pod.Name)
	_, err := p.Client.CoreV1().Pods(pod.Namespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
	return err
}

type podPreemptorImpl struct {
	Client clientset.Interface
}

func (p *podPreemptorImpl) getUpdatedPod(pod *v1.Pod) (*v1.Pod, error) {
	return p.Client.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
}

func (p *podPreemptorImpl) deletePod(pod *v1.Pod) error {
	return p.Client.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
}

func (p *podPreemptorImpl) setNominatedNodeName(pod *v1.Pod, nominatedNodeName string) error {
	podCopy := pod.DeepCopy()
	podCopy.Status.NominatedNodeName = nominatedNodeName
	_, err := p.Client.CoreV1().Pods(pod.Namespace).UpdateStatus(context.TODO(), podCopy, metav1.UpdateOptions{})
	return err
}

func (p *podPreemptorImpl) removeNominatedNodeName(pod *v1.Pod) error {
	if len(pod.Status.NominatedNodeName) == 0 {
		return nil
	}
	return p.setNominatedNodeName(pod, "")
}

func defaultAlgorithmSourceProviderName() *string {
	provider := schedulerapi.SchedulerDefaultProviderName
	return &provider
}
