package dynamicpodspec

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		client:     c,
		needUpdate: false,
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
	p.needUpdate = false
	return lifecycle.PodAdmitResult{
		Admit: true,
	}
}

func (p *podUpdater) update(pod *v1.Pod) error {
	if !p.needUpdate {
		return nil
	}
	klog.V(2).Infof("Updating pod %s/%s", pod.Namespace, pod.Name)
	oldPodUID := pod.GetUID()
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		spec := pod.Spec
		_, updateErr := p.client.CoreV1().Pods(pod.Namespace).Update(context.Background(), pod, metav1.UpdateOptions{})
		if updateErr == nil {
			return nil
		}
		if updated, err := p.client.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{ResourceVersion: "0"}); err == nil {
			newPodUID := updated.GetUID()
			if newPodUID != oldPodUID {
				return fmt.Errorf("pod was deleted and then recreated, skipping pod update, oldPodUID %s, newPodUID %s", oldPodUID, newPodUID)
			}
			pod = updated.DeepCopy()
			pod.Spec = spec
		}
		return updateErr
	})
	return retryErr
}

func (p *podUpdater) NeedUpdate() {
	p.needUpdate = true
}
