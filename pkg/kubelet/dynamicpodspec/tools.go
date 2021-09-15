package dynamicpodspec

import (
	"net"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

/*
Use listen to test the local port is available or not.
TODO add UDP support
*/
func isPortAvailable(network string, port int) bool {
	conn, err := net.Listen(strings.ToLower(network), ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func getScheduledTime(pod *corev1.Pod) time.Time {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled {
			if condition.Status == corev1.ConditionTrue {
				return condition.LastTransitionTime.Time
			}
		}
	}
	return time.Time{}
}

func getPodPorts(pod *corev1.Pod) map[int]string {
	var result = map[int]string{}
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			result[int(port.HostPort)] = string(port.Protocol)
		}
	}
	return result
}
