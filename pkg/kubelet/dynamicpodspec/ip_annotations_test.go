package dynamicpodspec

import (
	"errors"
	"fmt"
	"k8s.io/kubernetes/pkg/kubelet/lifecycle"
	"net"
	"testing"
)

func TestIpAnnotationsAdmit(t *testing.T) {
	for i := 0; i < 3; i++ {
		hostIPv4, hostIPv6, err := mockIPFuncRes(i)

		if err != nil {
			fmt.Printf("TestIpAnnotationsAdmit Admit failed : %v", lifecycle.PodAdmitResult{
				Admit:   false,
				Reason:  ForbiddenReason,
				Message: fmt.Sprintf("GetDualStackIPFromHostInterfaces failed: %v \n", err),
			})
		}

		var hostIPv4Str, hostIPv6Str string
		if hostIPv4 != nil {
			hostIPv4Str = hostIPv4.String()
		}
		if hostIPv6 != nil {
			hostIPv6Str = hostIPv6.String()
		}

		fmt.Printf("TestIpAnnotationsAdmit IPv4 : %v, IPv6 : %v \n", hostIPv4Str, hostIPv6Str)

		fmt.Printf("TestIpAnnotationsAdmit Admit success : %v \n", lifecycle.PodAdmitResult{
			Admit: true,
		})
	}
}

func mockIPFuncRes(fakeIndex int) (net.IP, net.IP, error) {
	switch fakeIndex {
	case 0:
		return net.IP{0xa, 0x8, 0x79, 0x51}, net.IP{0xfe, 0x80, 0x00, 0x00, 0xf8, 0x16, 0x3e, 0xff, 0xfe, 0x6a, 0x68, 0x2d, 0x00, 0x00, 0x00, 0x00}, nil
	case 1:
		return net.IP{0xa, 0x8, 0x79, 0x51}, nil, nil
	case 2:
		return nil, net.IP{0xfc, 0x80, 0x00, 0x00, 0xf8, 0x16, 0x3e, 0xff, 0xfe, 0x6a, 0x68, 0x2d, 0x00, 0x00, 0x00, 0x00}, nil
	case 3:
		return nil, nil, errors.New("unknown error")
	default:
		return nil, nil, errors.New("unknown error")
	}
}
