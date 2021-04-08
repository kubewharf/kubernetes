package podannotations

import (
	"context"
	"io"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apiserver/pkg/admission"
	api "k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/kubernetes/plugin/pkg/admission/godel"
)

const (
	// PluginName indicates name of admission plugin.
	PluginName = "GodelPodAnnotations"
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
	switch godel.GetPodLauncher(pod) {
	case godel.PodLauncherKubelet, godel.PodLauncherNodeManager:
		return nil
	case "":
		// set default launcher as kubelet
		godel.SetPodLauncher(pod, godel.PodLauncherKubelet)
		return nil
	default:
		return errors.NewBadRequest("invalid pod launcher")
	}
}

func (p *godelPodAnnotationsPlugin) admitPodResourceType(pod *apiv1.Pod) error {
	switch godel.GetPodResourceType(pod) {
	case godel.GuaranteedPod, godel.BestEffortPod:
		return nil
	case "":
		// set default resource type as guaranteed
		godel.SetPodResourceType(pod, godel.GuaranteedPod)
		return nil
	default:
		return errors.NewBadRequest("invalid pod resource type")
	}
}

func (p *godelPodAnnotationsPlugin) admitPodState(pod *apiv1.Pod) error {
	if godel.AbnormalPodState(pod) {
		return errors.NewBadRequest("godel pod state is abnormal")
	}
	if godel.PendingPod(pod) && len(godel.GetPodState(pod)) == 0 {
		godel.SetPodState(pod, godel.PodPending)
	}
	return nil
}
