package dynamicpodspec

import (
	"k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

type PodUpdater interface {
	Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult
	NeedUpdate()
}

type podUpdater struct {
	needUpdate bool
	client     clientset.Interface
}

func NewPodUpdater(c clientset.Interface) PodUpdater {
	return &podUpdater{
		client: c,
	}
}

func (p *podUpdater) Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
	if err := p.update(attrs.Pod); err != nil {
		klog.V(1).Infof("failed to update pod: %s/%s %v", attrs.Pod.Namespace, attrs.Pod.Name, err)
		return lifecycle.PodAdmitResult{
			Admit:   false,
			Reason:  "FailedUpdate",
			Message: "Update pod failed.",
		}
	}
	return lifecycle.PodAdmitResult{
		Admit: true,
	}
}

func (p *podUpdater) update(pod *v1.Pod) error {
	if !p.needUpdate {
		return nil
	}
	klog.V(2).Infof("Updating pod %s/%s", pod.Namespace, pod.Name)
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() (err error) {
		pod, err = p.client.CoreV1().Pods(pod.Namespace).Update(pod)
		return err
	})
	return retryErr
}

func (p *podUpdater) NeedUpdate() {
	p.needUpdate = true
}
