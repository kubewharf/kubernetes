package dynamicpodspec

import (
	"fmt"
	"sort"

	"net"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	netutil "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/klog"
	podutil "k8s.io/kubernetes/pkg/api/pod"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
)

const (
	familyIPv4 int = nl.FAMILY_V4
	familyIPv6 int = nl.FAMILY_V6

	ForbiddenReason = "GetNodeHostInterfaceError"
)

type sortableRoutes []*netlink.Route

func (r sortableRoutes) Len() int {
	return len(r)
}

func (r sortableRoutes) Less(i, j int) bool {
	return r[i].Priority < r[j].Priority
}

func (r sortableRoutes) Swap(i, j int) {
	r[i], r[j] = r[j], r[i]
}

var (
	dualStackHostIPv4 net.IP
	dualStackHostIPv6 net.IP
	dualStackIPErr    error
)

type PodAnnotationsAdmitHandler struct {
	podUpdater PodUpdater
}

func NewPodAnnotationsAdmitHandler(podUpdater PodUpdater) *PodAnnotationsAdmitHandler {
	return &PodAnnotationsAdmitHandler{podUpdater: podUpdater}
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

	klog.Infof("GetDualStackIPFromHostInterfaces %v ipv4: %v, ipv6: %v \n\n", attrs.Pod.Name, dualStackHostIPv4, dualStackHostIPv6)

	ipv4, ipv6 := attrs.Pod.ObjectMeta.Annotations[podutil.HostIPv4AnnotationKey], attrs.Pod.ObjectMeta.Annotations[podutil.HostIPv6AnnotationKey]
	if ipv4 != "" && ipv6 != "" {
		return lifecycle.PodAdmitResult{
			Admit: true,
		}
	}

	var hostIPv4Str, hostIPv6Str string
	if dualStackHostIPv4 != nil {
		hostIPv4Str = dualStackHostIPv4.String()
	}
	if dualStackHostIPv6 != nil {
		hostIPv6Str = dualStackHostIPv6.String()
	}

	if ipv4 == hostIPv4Str && ipv6 == hostIPv6Str {
		// nothing changed
		return lifecycle.PodAdmitResult{
			Admit: true,
		}
	}

	updatedAnnotation := make(map[string]string)
	for key, value := range attrs.Pod.Annotations {
		updatedAnnotation[key] = value
	}
	updatedAnnotation[podutil.HostIPv4AnnotationKey] = hostIPv4Str
	updatedAnnotation[podutil.HostIPv6AnnotationKey] = hostIPv6Str
	attrs.Pod.Annotations = updatedAnnotation

	p.podUpdater.NeedUpdate()
	return lifecycle.PodAdmitResult{
		Admit: true,
	}
}

func validateDefaultIPAddress(family int, ip net.IP) net.IP {
	// if host ipv4/ipv6 is local loopback like address, then just filtered them
	if ipValidateGlobal := validateGlobalIPAddress(family, ip); ipValidateGlobal == nil {
		return nil
	}
	addrByNetLink, err := getDefaultIPAddressByNetLink(family)
	if err != nil {
		klog.Errorf("GetDefaultIPAddressByNetLink error family %v : %v", family, err)
		return ip
	}
	klog.Infof("GetDefaultIPAddressByNetLink success family %v : %v", family, addrByNetLink.String())

	if !ip.Equal(addrByNetLink) {
		klog.Errorf("GetDefaultIPAddressByNetLink validate error family %v : %v, %v", family, ip.String(), addrByNetLink.String())
		return addrByNetLink
	}
	return ip
}

func validateGlobalIPAddress(family int, ip net.IP) net.IP {
	switch family {
	case netlink.FAMILY_V4:
		// If ip is not an IPv4 address, To4 returns nil.
		if ip == nil || ip.To4() == nil || !ip.IsGlobalUnicast() {
			return nil
		}
		return ip
	case netlink.FAMILY_V6:
		if ip == nil || ip.To4() != nil || !ip.IsGlobalUnicast() {
			return nil
		}
		return ip
	default:
		return nil
	}
}

func getDefaultIPAddressByNetLink(family int) (net.IP, error) {
	var dest net.IP
	switch family {
	case nl.FAMILY_V4:
		dest = net.ParseIP("114.114.114.114") //114dns
	case nl.FAMILY_V6:
		dest = net.ParseIP("240C::6644") //public dns
	default:
		return nil, fmt.Errorf("invalid ip family %d", family)
	}

	routes, err := netlink.RouteGet(dest)
	if err != nil {
		return nil, fmt.Errorf("get route for %s failed: %s", dest.String(), err)
	}

	var sortRoutes sortableRoutes
	for i := range routes {
		sortRoutes = append(sortRoutes, &routes[i])
	}
	sort.Sort(sort.Reverse(sortRoutes))

	if len(sortRoutes) == 0 {
		return nil, fmt.Errorf("no default route found for ip family %d", family)
	}

	ifIndex := sortRoutes[0].LinkIndex
	if ifIndex == 0 {
		return nil, fmt.Errorf("invalid link index 0")
	}

	nextHop := sortRoutes[0].Gw
	if nextHop == nil {
		return nil, fmt.Errorf("nexthop is empty")
	}

	srcIP := sortRoutes[0].Src

	// in some cases，src would be empty as below. In this conditions, we try to get global unicast addresses from all network interfaces,
	// and use mask to filter gw address.
	// # nsenter --net=/proc/227616/ns/net ip -6 route get 240C::6644
	// 240c::6644 from :: via 11::1 dev v1 metric 1024 pref medium
	if srcIP == nil {
		link, e := netlink.LinkByIndex(ifIndex)
		if e != nil {
			return nil, fmt.Errorf("get link for %d failed: %w", ifIndex, e)
		}

		addresses, e := netlink.AddrList(link, family)
		if e != nil {
			return nil, fmt.Errorf("get addresses for %d faield: %w", ifIndex, e)
		}

		globalUnicastAddressesMap := map[string]*net.IPNet{}
		for i := range addresses {
			addr := &addresses[i]
			if !addr.IP.IsGlobalUnicast() {
				continue
			}
			ipNet := &net.IPNet{
				IP:   addr.IP,
				Mask: addr.Mask,
			}
			globalUnicastAddressesMap[ipNet.String()] = ipNet

			srcIP = addr.IP
		}

		if len(globalUnicastAddressesMap) > 1 {
			for _, ipNet := range globalUnicastAddressesMap {
				if ipNet.IP.Mask(ipNet.Mask).Equal(nextHop.Mask(ipNet.Mask)) {
					srcIP = ipNet.IP
				}
			}
		}
	}
	if srcIP == nil {
		return nil, fmt.Errorf("colud not determine src ip")
	}

	return srcIP, nil
}
