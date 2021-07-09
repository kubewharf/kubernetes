package podpriority

import (
	"context"
	"fmt"
	"io"

	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/admission"
	genericadmissioninitializers "k8s.io/apiserver/pkg/admission/initializer"
	"k8s.io/client-go/informers"
	schedulingv1listers "k8s.io/client-go/listers/scheduling/v1"
	api "k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/kubernetes/pkg/apis/scheduling"
	"k8s.io/kubernetes/plugin/pkg/admission/godel"
)

const (
	// PluginName indicates name of admission plugin.
	PluginName = "GodelPodPriority"

	CanBePreemptedAnnotationKey = "godel.bytedance.com/can-be-preempted"
)

// Register registers a plugin
func Register(plugins *admission.Plugins) {
	plugins.Register(PluginName, func(config io.Reader) (admission.Interface, error) {
		return NewPlugin(), nil
	})
}

// Plugin is an implementation of admission.Interface.
type Plugin struct {
	*admission.Handler
	lister schedulingv1listers.PriorityClassLister
}

var _ admission.MutationInterface = &Plugin{}
var _ admission.ValidationInterface = &Plugin{}
var _ = genericadmissioninitializers.WantsExternalKubeInformerFactory(&Plugin{})

// NewPlugin creates a new priority admission plugin.
func NewPlugin() *Plugin {
	return &Plugin{
		Handler: admission.NewHandler(admission.Create, admission.Update, admission.Delete),
	}
}

// ValidateInitialization implements the InitializationValidator interface.
func (p *Plugin) ValidateInitialization() error {
	if p.lister == nil {
		return fmt.Errorf("%s requires a lister", PluginName)
	}
	return nil
}

// SetExternalKubeInformerFactory implements the WantsInternalKubeInformerFactory interface.
func (p *Plugin) SetExternalKubeInformerFactory(f informers.SharedInformerFactory) {
	priorityInformer := f.Scheduling().V1().PriorityClasses()
	p.lister = priorityInformer.Lister()
	p.SetReadyFunc(priorityInformer.Informer().HasSynced)
}

var (
	podResource           = api.Resource("pods")
	priorityClassResource = scheduling.Resource("priorityclasses")
)

// Admit checks Pods and admits or rejects them. It also resolves the priority of pods based on their PriorityClass.
// Note that pod validation mechanism prevents update of a pod priority.
func (p *Plugin) Admit(ctx context.Context, a admission.Attributes, o admission.ObjectInterfaces) error {
	operation := a.GetOperation()
	// Ignore all calls to subresources
	if len(a.GetSubresource()) != 0 {
		return nil
	}
	switch a.GetResource().GroupResource() {
	case podResource:
		if operation == admission.Create || operation == admission.Update {
			return p.admitGodelPodPriority(a)
		}
		return nil

	default:
		return nil
	}
}

// Validate checks PriorityClasses and admits or rejects them.
func (p *Plugin) Validate(ctx context.Context, a admission.Attributes, o admission.ObjectInterfaces) error {
	operation := a.GetOperation()
	// Ignore all calls to subresources
	if len(a.GetSubresource()) != 0 {
		return nil
	}

	switch a.GetResource().GroupResource() {
	case priorityClassResource:
		if operation == admission.Create || operation == admission.Update {
			return p.validatePriorityClass(a)
		}
		return nil

	default:
		return nil
	}
}

func (p *Plugin) admitGodelPodPriority(a admission.Attributes) error {
	operation := a.GetOperation()
	pod, ok := a.GetObject().(*api.Pod)
	if !ok {
		return errors.NewBadRequest("resource was marked with kind Pod but was unable to be converted")
	}

	if operation == admission.Update {
		oldPod, ok := a.GetOldObject().(*api.Pod)
		if !ok {
			return errors.NewBadRequest("resource was marked with kind Pod but was unable to be converted")
		}

		// This admission plugin set pod.Spec.Priority on create.
		// Ensure the existing priority is preserved on update.
		// API validation prevents mutations to Priority and PriorityClassName, so any other changes will fail update validation and not be persisted.
		if pod.Spec.Priority == nil && oldPod.Spec.Priority != nil {
			pod.Spec.Priority = oldPod.Spec.Priority
		}
		return nil
	}

	if operation == admission.Create {
		var priority int32
		canBePreempted := "false"
		if len(pod.Spec.PriorityClassName) == 0 {
			var err error
			var pcName string
			pcName, priority, canBePreempted, err = p.getDefaultPriorityForGodelPod(pod)
			if err != nil {
				return fmt.Errorf("failed to get default priority class: %v", err)
			}
			pod.Spec.PriorityClassName = pcName
		} else {
			// Try resolving the priority class name.
			pc, err := p.lister.Get(pod.Spec.PriorityClassName)
			if err != nil {
				if errors.IsNotFound(err) {
					return admission.NewForbidden(a, fmt.Errorf("no PriorityClass with name %v was found", pod.Spec.PriorityClassName))
				}

				return fmt.Errorf("failed to get PriorityClass with name %s: %v", pod.Spec.PriorityClassName, err)
			}

			if err := passPodPriorityCheckForGodelPod(pod, pc); err != nil {
				return fmt.Errorf("failed to pass pod priority check for godel pod: %v", err)
			}
			priority = pc.Value
			if pc.Annotations != nil && pc.Annotations[CanBePreemptedAnnotationKey] == "true" {
				canBePreempted = "true"
			}
		}
		// if the pod contained a priority that differs from the one computed from the priority class, error
		if pod.Spec.Priority != nil && *pod.Spec.Priority != priority {
			return admission.NewForbidden(a, fmt.Errorf("the integer value of priority (%d) must not be provided in pod spec; priority admission controller computed %d from the given PriorityClass name", *pod.Spec.Priority, priority))
		}
		pod.Spec.Priority = &priority

		if pod.Annotations == nil {
			pod.Annotations = make(map[string]string)
		}
		pod.Annotations[CanBePreemptedAnnotationKey] = canBePreempted
	}
	return nil
}

// validatePriorityClass ensures that the value field is not larger than the highest user definable priority.
// If the DefaultPriorityTypeAnnotationKey is set, it ensures that there is no other PriorityClass whose DefaultPriorityTypeAnnotationKey is set as the same priority type.
func (p *Plugin) validatePriorityClass(a admission.Attributes) error {
	operation := a.GetOperation()
	pc, ok := a.GetObject().(*scheduling.PriorityClass)
	if !ok {
		return errors.NewBadRequest("resource was marked with kind PriorityClass but was unable to be converted")
	}
	// If the new PriorityClass tries to be the default priority, make sure that no other priority class is marked as default.
	if priorityType := pc.Annotations[godel.DefaultPriorityTypeAnnotationKey]; priorityType != "" {
		dpc, err := p.getDefaultPriorityClassByPriorityType(priorityType)
		if err != nil {
			return fmt.Errorf("failed to get default priority class for priority type: %s, err: %v", priorityType, err)
		}
		if dpc != nil {
			// Throw an error if a second default priority class is being created, or an existing priority class is being marked as default, while another default already exists.
			if operation == admission.Create || (operation == admission.Update && dpc.GetName() != pc.GetName()) {
				return admission.NewForbidden(a, fmt.Errorf("PriorityClass %v is already marked as default for priority type: %s. Only one default can exist", dpc.GetName(), priorityType))
			}
		}
	}
	return nil
}

func (p *Plugin) getDefaultPriorityClassByPriorityType(priorityType string) (*schedulingv1.PriorityClass, error) {
	list, err := p.lister.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	// In case more than one default priority class is added as a result of a race condition,
	// we pick the one with the lowest priority value.
	var defaultPC *schedulingv1.PriorityClass
	for _, pci := range list {
		if pci.Annotations[godel.DefaultPriorityTypeAnnotationKey] == priorityType {
			if defaultPC == nil || defaultPC.Value > pci.Value {
				defaultPC = pci
			}
		}
	}
	return defaultPC, nil
}

func (p *Plugin) getDefaultPriorityForGodelPod(pod *api.Pod) (string, int32, string, error) {
	priorityType := godel.GetPodPriorityType(pod)
	canBePreempted := "false"
	// get default priority class
	dpc, err := p.getDefaultPriorityClassByPriorityType(priorityType)
	if err != nil {
		return "", 0, canBePreempted, err
	}
	if dpc != nil {
		if dpc.Annotations != nil && dpc.Annotations[CanBePreemptedAnnotationKey] == "true" {
			canBePreempted = "true"
		}
		return dpc.Name, dpc.Value, canBePreempted, nil
	}

	canBePreempted = "true"
	switch priorityType {
	case godel.PriorityGuaranteedKublet:
		return "", godel.MinPriorityForGuaranteedKublet, canBePreempted, nil
	case godel.PriorityGuaranteedNodeManager:
		return "", godel.MinPriorityForGuaranteedNodeManager, canBePreempted, nil
	case godel.PriorityBestEffortKublet:
		return "", godel.MinPriorityForBestEffortKublet, canBePreempted, nil
	case godel.PriorityBestEffortNodeManager:
		return "", godel.MinPriorityForBestEffortNodeManager, canBePreempted, nil
	default:
		return "", int32(scheduling.DefaultPriorityWhenNoDefaultClassExists), canBePreempted, nil
	}
}

func passPodPriorityCheckForGodelPod(pod *api.Pod, pc *schedulingv1.PriorityClass) error {
	priorityType := godel.GetPodPriorityType(pod)
	var minValue, maxValue int32
	switch priorityType {
	case godel.PriorityGuaranteedKublet:
		minValue = godel.MinPriorityForGuaranteedKublet
		maxValue = godel.MaxPriorityForGuaranteedKublet
		break
	case godel.PriorityGuaranteedNodeManager:
		minValue = godel.MinPriorityForGuaranteedNodeManager
		maxValue = godel.MaxPriorityForGuaranteedNodeManager
		break
	case godel.PriorityBestEffortKublet:
		minValue = godel.MinPriorityForBestEffortKublet
		maxValue = godel.MaxPriorityForBestEffortKublet
		break
	case godel.PriorityBestEffortNodeManager:
		minValue = godel.MinPriorityForBestEffortNodeManager
		maxValue = godel.MaxPriorityForBestEffortNodeManager
		break
	default:
		return nil
	}
	if pc.Value < minValue || pc.Value > maxValue {
		return fmt.Errorf("the priority value for type %s should be in range [%d, %d]", priorityType, minValue, maxValue)
	}
	return nil
}
