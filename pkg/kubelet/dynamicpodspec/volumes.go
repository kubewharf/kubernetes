package dynamicpodspec

import (
	"strings"

	"k8s.io/klog"
	utilpod "k8s.io/kubernetes/pkg/api/pod"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

type assignVolumeAdmitHandler struct {
	podUpdater PodUpdater
}

func NewAssignVolumeAdmitHandler(podUpdater PodUpdater) *assignVolumeAdmitHandler {
	return &assignVolumeAdmitHandler{
		podUpdater: podUpdater,
	}
}

func (w *assignVolumeAdmitHandler) Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
	pod := attrs.Pod
	// For the pods such as host daemon agent disable insert hack volume.
	if _, ok := pod.GetAnnotations()[utilpod.PodAutoPortAnnotation]; !ok {
		return lifecycle.PodAdmitResult{
			Admit: true,
		}
	}
	volumeListStr, ok := pod.GetAnnotations()[utilpod.PodHostPathTemplateAnnotation]
	if !ok {
		return lifecycle.PodAdmitResult{
			Admit: true,
		}
	}
	volumeList := strings.Split(volumeListStr, ",")
	volumeSet := make(map[string]struct{})
	for _, volumeName := range volumeList {
		volumeSet[volumeName] = struct{}{}
	}
	psm, ok := pod.Labels["psm"]
	if !ok || len(psm) <= 0 {
		return lifecycle.PodAdmitResult{
			Admit: true,
		}
	}
	for _, volume := range pod.Spec.Volumes {
		if _, ok := volumeSet[volume.Name]; !ok {
			continue
		}
		if volume.HostPath == nil {
			klog.Warningf("Match volume name: %s successful, but HostPath is nil", volume.Name)
			continue
		}
		newPath := insertPodNameAfterPSM(volume.HostPath.Path, pod.Name, psm)
		if volume.HostPath.Path != newPath {
			volume.HostPath.Path = newPath
			w.podUpdater.NeedUpdate()
		}
		klog.V(5).Infof("%s/%s update volume %s", pod.Namespace, pod.Name, volume.HostPath.Path)
	}
	return lifecycle.PodAdmitResult{
		Admit: true,
	}
}

func insertPodNameAfterPSM(path, podName, psm string) string {
	if len(path) > 0 {
		part := strings.Split(path, "/")
		for i := range part {
			// Compatible code, scheduler may hack the volume in old code, avoiding duplicate hack.
			if part[i] == psm && (i+1) < len(path) && part[i+1] != podName {
				rear := append([]string{}, part[i+1:]...)
				part = append(part[0:i+1], podName)
				part = append(part, rear...)
				break
			}
		}
		return strings.Join(part, "/")
	}
	return path
}
