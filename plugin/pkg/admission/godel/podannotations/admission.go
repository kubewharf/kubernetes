package podannotations

import (
	"context"
	"io"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apiserver/pkg/admission"
	api "k8s.io/kubernetes/pkg/apis/core"
)

const (
	// PluginName indicates name of admission plugin.
	PluginName = "GodelPodAnnotations"

	// PodStateAnnotationKey is a pod annotation key, value is the pod state
	PodStateAnnotationKey = "godel.bytedance.com/pod-state"

	// PodResourceTypeAnnotationKey is a pod annotation key, value is the pod resource type (guaranteed or best-effort)
	PodResourceTypeAnnotationKey = "godel.bytedance.com/pod-resource-type"

	// PodLauncherAnnotationKey is a pod annotation key, value is the launcher of this pod (kubelet or node-manager)
	PodLauncherAnnotationKey = "godel.bytedance.com/pod-launcher"

	// SchedulerAnnotationKey is a pod annotation key, value is the scheduler id who is responsible for scheduling this pod
	SchedulerAnnotationKey = "selectedScheduler"

	// AssumedNodeAnnotationKey is a pod annotation key, value is the assumed node name chosen by one scheduler
	// the scheduler will reserve the allocated resource for the pod.
	AssumedNodeAnnotationKey = "assumedNode"

	// NominatedNodeAnnotationKey is a pod annotation key,
	// value is the node name chosen by scheduler for placing the pending pod by evicting others
	// value can be like: {node: node1, victims: pod1, pod2...}
	// the scheduler will reserve the allocated resource for the pod.
	NominatedNodeAnnotationKey = "nominatedNode"

	// pod launchers
	PodLauncherKubelet     = "kubelet"
	PodLauncherNodeManager = "node-manager"

	// pod resource types
	GuaranteedPod = "guaranteed"
	BestEffortPod = "best-effort"

	// pod states
	PodPending    = "Pending"
	PodDispatched = "Dispatched"
	PodAssumed    = "Assumed"
)

// Register registers a plugin
func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(config io.Reader) (admission.Interface, error) {
		return NewPlugin(), nil
	})
}

type godelPodAnnotationsPlugin struct {
	*admission.Handler
}

var _ admission.MutationInterface = &godelPodAnnotationsPlugin{}

// NewPlugin creates a new godel pod annotations admission plugin.
func NewPlugin() *godelPodAnnotationsPlugin {
	return &godelPodAnnotationsPlugin{
		Handler: admission.NewHandler(admission.Create, admission.Update),
	}
}

var (
	podResource = api.Resource("pods")
)

// Admit checks the admission policy and triggers corresponding actions
func (p *godelPodAnnotationsPlugin) Admit(ctx context.Context, a admission.Attributes, o admission.ObjectInterfaces) error {
	// Ignore all calls to subresources or resources other than pods.
	if len(a.GetSubresource()) != 0 || a.GetResource().GroupResource() != podResource {
		return nil
	}
	// Ignore all operations other than CREATE and UPDATE.
	if operation := a.GetOperation(); operation != admission.Create && operation != admission.Update {
		return nil
	}
	pod, ok := a.GetObject().(*apiv1.Pod)
	if !ok {
		return errors.NewBadRequest("Resource was marked with kind Pod but was unable to be converted")
	}

	if err := p.admitPodLauncher(pod); err != nil {
		return err
	}
	if err := p.admitPodResourceType(pod); err != nil {
		return err
	}
	if err := p.admitPodState(pod); err != nil {
		return err
	}
	return nil
}

func (p *godelPodAnnotationsPlugin) admitPodLauncher(pod *apiv1.Pod) error {
	switch pod.Annotations[PodLauncherAnnotationKey] {
	case PodLauncherKubelet, PodLauncherNodeManager:
		return nil
	case "":
		// set default launcher as kubelet
		setPodAnnotation(pod, PodLauncherAnnotationKey, PodLauncherKubelet)
		return nil
	default:
		return errors.NewBadRequest("invalid pod launcher")
	}
}

func (p *godelPodAnnotationsPlugin) admitPodResourceType(pod *apiv1.Pod) error {
	switch pod.Annotations[PodResourceTypeAnnotationKey] {
	case GuaranteedPod, BestEffortPod:
		return nil
	case "":
		// set default resource type as guaranteed
		setPodAnnotation(pod, PodResourceTypeAnnotationKey, GuaranteedPod)
		return nil
	default:
		return errors.NewBadRequest("invalid pod resource type")
	}
}

func (p *godelPodAnnotationsPlugin) admitPodState(pod *apiv1.Pod) error {
	if AbnormalPodState(pod) {
		return errors.NewBadRequest("godel pod state is abnormal")
	}
	if PendingPod(pod) && len(pod.Annotations[PodStateAnnotationKey]) == 0 {
		setPodAnnotation(pod, PodStateAnnotationKey, PodPending)
	}
	return nil
}

func setPodAnnotation(pod *apiv1.Pod, key, value string) {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[key] = value
}

// PendingPod checks if the given pod is in pending state
func PendingPod(pod *apiv1.Pod) bool {
	if pod.Annotations != nil &&
		(pod.Annotations[PodStateAnnotationKey] == PodPending || len(pod.Annotations[PodStateAnnotationKey]) == 0) &&
		len(pod.Annotations[SchedulerAnnotationKey]) == 0 &&
		len(pod.Annotations[AssumedNodeAnnotationKey]) == 0 &&
		len(pod.Annotations[NominatedNodeAnnotationKey]) == 0 &&
		len(pod.Spec.NodeName) == 0 {
		return true
	}
	return false
}

// DispatchedPod checks if the given pod is in dispatched state
func DispatchedPod(pod *apiv1.Pod) bool {
	if pod.Annotations != nil &&
		pod.Annotations[PodStateAnnotationKey] == PodDispatched &&
		len(pod.Annotations[SchedulerAnnotationKey]) != 0 &&
		len(pod.Annotations[AssumedNodeAnnotationKey]) == 0 &&
		len(pod.Annotations[NominatedNodeAnnotationKey]) == 0 &&
		len(pod.Spec.NodeName) == 0 {
		return true
	}
	return false
}

// assumedOrNominatedNodeIsSet checks if the AssumedNodeAnnotationKey or NominatedNodeAnnotationKey is set
func assumedOrNominatedNodeIsSet(pod *apiv1.Pod) bool {
	if pod.Annotations != nil {
		if len(pod.Annotations[AssumedNodeAnnotationKey]) == 0 && len(pod.Annotations[NominatedNodeAnnotationKey]) != 0 {
			return true
		}
		if len(pod.Annotations[AssumedNodeAnnotationKey]) != 0 && len(pod.Annotations[NominatedNodeAnnotationKey]) == 0 {
			return true
		}
	}
	return false
}

// AssumedPod checks if the given pod is in assumed state
func AssumedPod(pod *apiv1.Pod) bool {
	if pod.Annotations != nil &&
		pod.Annotations[PodStateAnnotationKey] == PodAssumed &&
		len(pod.Annotations[SchedulerAnnotationKey]) != 0 &&
		assumedOrNominatedNodeIsSet(pod) &&
		len(pod.Spec.NodeName) == 0 {
		return true
	}
	return false
}

// BoundPod checks if the given pod is bound
func BoundPod(pod *apiv1.Pod) bool {
	return len(pod.Spec.NodeName) != 0
}

// AbnormalPodState checks if the given pod is in abnormal state
func AbnormalPodState(pod *apiv1.Pod) bool {
	if BoundPod(pod) {
		return false
	}

	switch pod.Annotations[PodStateAnnotationKey] {
	case "", PodPending:
		if !PendingPod(pod) {
			return true
		} else {
			return false
		}
	case PodDispatched:
		if !DispatchedPod(pod) {
			return true
		} else {
			return false
		}
	case PodAssumed:
		if !AssumedPod(pod) {
			return true
		} else {
			return false
		}
	default:
		return true
	}
}
