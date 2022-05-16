package hostdualstackip

import (
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
)

const (
	ConsulHTTPHost = "CONSUL_HTTP_HOST"
)

var (
	hostIPList   = []string{TceMyHostIP, TceBytedHostIP, ConsulHTTPHost}
	hostIPv6List = []string{TceMyHostIPv6, TceBytedHostIPv6}
)

// OverridePodIPRelatedEnvs overrides pod ip related env in need.
func OverridePodIPRelatedEnvs(envVars []*runtimeapi.KeyValue, podIPs []string) []*runtimeapi.KeyValue {
	ip, ipv6 := ExtractPodDualStackIPsFromPodIPs(podIPs)
	var overrideList []*runtimeapi.KeyValue

	overrideList = append(overrideList, &runtimeapi.KeyValue{
		Key:   TceMyPodIP,
		Value: ip,
	})

	overrideList = append(overrideList, &runtimeapi.KeyValue{
		Key:   TceMyPodIPv6,
		Value: ipv6,
	})

	return replaceEnvValues(envVars, overrideList)
}

// OverrideHostIPRelatedEnvsFromPodIPs overrides host ip related env from pod ips in need.
func OverrideHostIPRelatedEnvsFromPodIPs(envVars []*runtimeapi.KeyValue, podIPs []string) []*runtimeapi.KeyValue {
	ip, ipv6 := ExtractPodDualStackIPsFromPodIPs(podIPs)
	var overrideList []*runtimeapi.KeyValue

	for _, key := range hostIPList {
		overrideList = append(overrideList, &runtimeapi.KeyValue{
			Key:   key,
			Value: ip,
		})
	}

	for _, key := range hostIPv6List {
		overrideList = append(overrideList, &runtimeapi.KeyValue{
			Key:   key,
			Value: ipv6,
		})
	}

	return replaceEnvValues(envVars, overrideList)
}

func replaceEnvValues(defaults []*runtimeapi.KeyValue, overrides []*runtimeapi.KeyValue) []*runtimeapi.KeyValue {
	cache := make(map[string]int, len(defaults))
	results := make([]*runtimeapi.KeyValue, 0, len(defaults))
	for i, e := range defaults {
		results = append(results, e)
		cache[e.Key] = i
	}

	for _, value := range overrides {
		// Just do a normal set/update
		if i, exists := cache[value.Key]; exists {
			results[i] = value
		}
	}

	return results
}
