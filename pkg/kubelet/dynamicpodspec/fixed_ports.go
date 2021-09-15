package dynamicpodspec

import (
	"fmt"

	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

type fixedPortAdmitHandler struct {
}

func NewFixedPortHandler() lifecycle.PodAdmitHandler {
	return &fixedPortAdmitHandler{}
}

func (f *fixedPortAdmitHandler) Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
	var (
		passResult = lifecycle.PodAdmitResult{
			Admit: true,
		}
	)

	pod := attrs.Pod
	// only for host network pod
	if !pod.Spec.HostNetwork {
		return passResult
	}
	ports := getPodPorts(pod)
	for portNum, portType := range ports {
		if portNum == 0 {
			return passResult
		}
		if !isPortAvailable(portType, portNum) {
			return lifecycle.PodAdmitResult{
				Admit:   false,
				Reason:  "HostPortNotAvailable",
				Message: fmt.Sprintf("%d [%s] cannot be allocated", portNum, portType),
			}
		}
	}
	return passResult
}
