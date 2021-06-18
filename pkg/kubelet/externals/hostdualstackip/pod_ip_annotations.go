package hostdualstackip

import (
	"fmt"
	"net"

	netutil "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

const (
	HostIPv4AnnotationKey = "pod.tce.kubernetes.io/host.ipv4"
	HostIPv6AnnotationKey = "pod.tce.kubernetes.io/host.ipv6"
	PodIPv6AnnotationKey  = "pod.tce.kubernetes.io/pod.ipv6"

	ForbiddenReason = "GetNodeHostInterfaceError"
)

var (
	dualStackHostIPv4 net.IP
	dualStackHostIPv6 net.IP
	dualStackIPErr    error
)

type PodAnnotationsAdmitHandler struct {
}

func NewPodAnnotationsAdmitHandler() *PodAnnotationsAdmitHandler {
	return &PodAnnotationsAdmitHandler{}
}

func init() {
	// init GetDualStackIPFromHostInterfaces IP addresses here.
	// in some cases, a machine may got IPv6 address in running time, but not set the address to docker config or other CNI config,
	// it may cause container ENV got HOST_IPV6, but not got POD_IPV6, that makes some module(like mesh agent) fallback listen to
	// node's IPV6 address, not suitable and failed.
	_ = setDualStackHostIPs()
}

func setDualStackHostIPs() error {
	dualStackHostIPv4, dualStackHostIPv6, dualStackIPErr = netutil.GetDualStackIPFromHostInterfaces()
	if dualStackIPErr != nil {
		klog.Errorf("GetDualStackIPFromHostInterfaces ip addresses error : %v", dualStackIPErr)
		return dualStackIPErr
	}

	// when a host has multi network interfaces which got valid global ipv6 addresses and default route [::/0] on it,
	// like eth0 and eth1, and eth1 usually be as strategy route table and eth0 as default route table,
	// so in file /proc/net/ipv6_route, eth1 route rule will be on top of eth0's route which will cause
	// `getIPv6DefaultRoutes` in k8s.io/apimachinery/pkg/util/net/interface.go get wrong default route for ipv6 family.
	// We add validate func here to check whether the default ip addresses are equal to address got from netlink.
	dualStackHostIPv4 = validateDefaultIPAddress(familyIPv4, dualStackHostIPv4)
	dualStackHostIPv6 = validateDefaultIPAddress(familyIPv6, dualStackHostIPv6)
	return nil
}

func (p *PodAnnotationsAdmitHandler) Admit(attrs *lifecycle.PodAdmitAttributes) lifecycle.PodAdmitResult {
	if dualStackIPErr != nil {
		// retry setDualStackHostIP, prevent some init error in GetDualStackIPFromHostInterfaces
		err := setDualStackHostIPs()
		if err != nil {
			return lifecycle.PodAdmitResult{
				Admit:   false,
				Reason:  ForbiddenReason,
				Message: fmt.Sprintf("GetDualStackIPFromHostInterfaces failed: %v", dualStackIPErr),
			}
		}
	}

	klog.Infof("GetDualStackIPFromHostInterfaces %v ipv4: %v, ipv6: %v \n\n", attrs.Pod.Name, dualStackHostIPv4, dualStackHostIPv4)

	var hostIPv4Str, hostIPv6Str string
	if dualStackHostIPv4 != nil {
		hostIPv4Str = dualStackHostIPv4.String()
	}
	if dualStackHostIPv6 != nil {
		hostIPv6Str = dualStackHostIPv6.String()
	}
	attrs.Pod.ObjectMeta.Annotations[HostIPv4AnnotationKey] = hostIPv4Str
	attrs.Pod.ObjectMeta.Annotations[HostIPv6AnnotationKey] = hostIPv6Str

	return lifecycle.PodAdmitResult{
		Admit: true,
	}
}

func determinateIPFamily(ip string) (int, error) {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return 0, fmt.Errorf("determinateIPFamily Parse IP error : %v ", ip)
	}
	if parsedIP.To4() != nil {
		return familyIPv4, nil
	}
	if parsedIP.To16() != nil {
		return familyIPv6, nil
	}
	return 0, fmt.Errorf("determinateIPFamily Parse IP %v : unknown family ", ip)
}

func ExtractPodDualStackIPsFromPodIPs(podIPs []string) (podIPv4, podIPv6 string) {
	for _, podIP := range podIPs {
		ipFamily, err := determinateIPFamily(podIP)
		if err != nil {
			klog.Errorf("determinateIPFamily %v error : %v \n\n", podIP, err)
			continue
		}
		if ipFamily == familyIPv4 {
			podIPv4 = podIP
		}
		if ipFamily == familyIPv6 {
			podIPv6 = podIP
		}
	}
	return
}
