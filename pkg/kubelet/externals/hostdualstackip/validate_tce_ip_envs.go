package hostdualstackip

import (
	"net"

	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
)

const (
	TceMyHostIP      = "MY_HOST_IP"
	TceMyHostIPv6    = "MY_HOST_IPV6"
	TceBytedHostIP   = "BYTED_HOST_IP"
	TceBytedHostIPv6 = "BYTED_HOST_IPV6"
	TceMyPodIP       = "MY_POD_IP"
	TceMyPodIPv6     = "MY_POD_IPv6"
)

var (
	validateEnvFunc map[string]func(kubecontainer.EnvVar) kubecontainer.EnvVar
)

func validateIPv4EnvVar(envVar kubecontainer.EnvVar) kubecontainer.EnvVar {
	ip := net.ParseIP(envVar.Value)
	// If ip is not an IPv4 address, To4 returns nil.
	if ip == nil || ip.To4() == nil {
		envVar.Value = ""
	}
	return envVar
}

// IPv6 address should be globalUnicast in env value
func validateGlobalIPv4EnvVar(envVar kubecontainer.EnvVar) kubecontainer.EnvVar {
	ip := net.ParseIP(envVar.Value)
	// If ip is not an IPv4 address, To4 returns nil.
	if ip == nil || ip.To4() == nil || !ip.IsGlobalUnicast() {
		envVar.Value = ""
	}
	return envVar
}

func validateIPv6EnvVar(envVar kubecontainer.EnvVar) kubecontainer.EnvVar {
	ip := net.ParseIP(envVar.Value)
	// If ip is not an IPv4 address, To4 returns nil.
	if ip == nil || ip.To4() != nil {
		envVar.Value = ""
	}
	return envVar
}

// IPv6 address should be globalUnicast in env value
func validateGlobalIPv6EnvVar(envVar kubecontainer.EnvVar) kubecontainer.EnvVar {
	ip := net.ParseIP(envVar.Value)
	// If ip is not an IPv4 address, To4 returns nil.
	if ip == nil || ip.To4() != nil || !ip.IsGlobalUnicast() {
		envVar.Value = ""
	}
	return envVar
}

func init() {
	validateEnvFunc = map[string]func(kubecontainer.EnvVar) kubecontainer.EnvVar{
		TceMyHostIP:      validateGlobalIPv4EnvVar,
		TceMyHostIPv6:    validateGlobalIPv6EnvVar,
		TceBytedHostIP:   validateGlobalIPv4EnvVar,
		TceBytedHostIPv6: validateGlobalIPv6EnvVar,
		TceMyPodIP:       validateIPv4EnvVar,
		TceMyPodIPv6:     validateGlobalIPv6EnvVar,
	}
}

// validate ip-related container's environments which used in TCE
func ValidateIPRelatedEnvs(envVars []kubecontainer.EnvVar) []kubecontainer.EnvVar {
	if len(envVars) == 0 {
		return envVars
	}

	validatedEnvVars := make([]kubecontainer.EnvVar, 0)
	for _, envVar := range envVars {
		validateFn, ok := validateEnvFunc[envVar.Name]
		if !ok {
			validatedEnvVars = append(validatedEnvVars, envVar)
			continue
		}
		validatedEnvVars = append(validatedEnvVars, validateFn(envVar))
	}
	return validatedEnvVars
}
