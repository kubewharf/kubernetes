package hostdualstackip

import (
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
)

const (
	CloudNativeIPEnvKey   = "CLOUDNATIVE_INET_ADDR"
	CloudNativeIPv6EnvKey = "CLOUDNATIVE_INET_ADDR_IPV6"

	ConsulHTTPHost = "CONSUL_HTTP_HOST"
)

var (
	hostIPList   = []string{TceMyHostIP, TceBytedHostIP, ConsulHTTPHost}
	hostIPv6List = []string{TceMyHostIPv6, TceBytedHostIPv6}

	overridesHostIPKeyMap = map[string][]string{
		CloudNativeIPEnvKey:   hostIPList,
		CloudNativeIPv6EnvKey: hostIPv6List,
	}

	overridesPodIPKeyMap = map[string][]string{
		CloudNativeIPEnvKey:   {TceMyPodIP},
		CloudNativeIPv6EnvKey: {TceMyPodIPv6},
	}
)

// OverrideHostIPRelatedEnvs overrides ip related env in need.
func OverrideHostIPRelatedEnvs(envVars []kubecontainer.EnvVar) []kubecontainer.EnvVar {
	var overrideList []kubecontainer.EnvVar
	for _, env := range envVars {
		if keyList, ok := overridesHostIPKeyMap[env.Name]; ok {
			for _, key := range keyList {
				overrideList = append(overrideList, kubecontainer.EnvVar{
					Name:  key,
					Value: env.Value,
				})
			}
		}
	}

	return replaceEnvValues(envVars, overrideList)
}

// OverridePodIPRelatedEnvs overrides pod ip related env in need.
func OverridePodIPRelatedEnvs(envVars []kubecontainer.EnvVar) []kubecontainer.EnvVar {
	var overrideList []kubecontainer.EnvVar
	for _, env := range envVars {
		if keyList, ok := overridesPodIPKeyMap[env.Name]; ok {
			for _, key := range keyList {
				overrideList = append(overrideList, kubecontainer.EnvVar{
					Name:  key,
					Value: env.Value,
				})
			}
		}
	}

	return replaceEnvValues(envVars, overrideList)
}

func replaceEnvValues(defaults []kubecontainer.EnvVar, overrides []kubecontainer.EnvVar) []kubecontainer.EnvVar {
	cache := make(map[string]int, len(defaults))
	results := make([]kubecontainer.EnvVar, 0, len(defaults))
	for i, e := range defaults {
		results = append(results, e)
		cache[e.Name] = i
	}

	for _, value := range overrides {
		// Just do a normal set/update
		if i, exists := cache[value.Name]; exists {
			results[i] = value
		}
	}

	return results
}
