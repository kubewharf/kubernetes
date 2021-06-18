package hostdualstackip

import (
	"fmt"
	"net"
	"sort"

	"k8s.io/klog"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
)

const (
	familyIPv4 int = nl.FAMILY_V4
	familyIPv6 int = nl.FAMILY_V6
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
