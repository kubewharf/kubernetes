package dynamicpodspec

import (
	"net"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func Test_isPortAvailable(t *testing.T) {
	type args struct {
		network string
		port    int
	}
	l, err := net.Listen("tcp", ":7001")
	if err != nil {
		t.Errorf("cannot make listend port: %v", err)
	}

	defer func() {
		_ = l.Close()
	}()

	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "normal test",
			args: args{
				network: "TCP",
				port:    7000,
			},
			want: true,
		},
		{
			name: "normal test",
			args: args{
				network: "TCP",
				port:    7001,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPortAvailable(tt.args.network, tt.args.port); got != tt.want {
				t.Errorf("isPortAvailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getPodPorts(t *testing.T) {
	type args struct {
		pod *corev1.Pod
	}
	tests := []struct {
		name string
		args args
		want map[int]string
	}{
		{
			name: "no container",
			args: args{
				pod: &corev1.Pod{
					Spec: corev1.PodSpec{},
				},
			},
			want: map[int]string{},
		},
		{
			name: "container no ports",
			args: args{
				pod: &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Ports: []corev1.ContainerPort{},
							},
						},
					},
				},
			},
			want: map[int]string{},
		},
		{
			name: "container with one port",
			args: args{
				pod: &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Ports: []corev1.ContainerPort{
									{
										Name:          "ut",
										HostPort:      7000,
										ContainerPort: 7000,
										Protocol:      corev1.ProtocolTCP,
									},
								},
							},
						},
					},
				},
			},
			want: map[int]string{
				7000: "TCP",
			},
		},
		{
			name: "container with multi port",
			args: args{
				pod: &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Ports: []corev1.ContainerPort{
									{
										Name:          "ut",
										HostPort:      7000,
										ContainerPort: 7000,
										Protocol:      corev1.ProtocolTCP,
									},
								},
							},
						},
					},
				},
			},
			want: map[int]string{
				7000: "TCP",
			},
		},
		{
			name: "multi containers with multi ports",
			args: args{
				pod: &corev1.Pod{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Ports: []corev1.ContainerPort{
									{
										Name:          "ut",
										HostPort:      7000,
										ContainerPort: 7000,
										Protocol:      corev1.ProtocolTCP,
									},
									{
										Name:          "ut-2",
										HostPort:      7001,
										ContainerPort: 6000,
										Protocol:      corev1.ProtocolTCP,
									},
								},
							},
							{
								Ports: []corev1.ContainerPort{
									{
										Name:          "ut-03",
										HostPort:      7003,
										ContainerPort: 7003,
										Protocol:      corev1.ProtocolTCP,
									},
									{
										Name:          "ut-2",
										HostPort:      7004,
										ContainerPort: 6004,
										Protocol:      corev1.ProtocolUDP,
									},
								},
							},
						},
					},
				},
			},
			want: map[int]string{
				7000: "TCP",
				7001: "TCP",
				7003: "TCP",
				7004: "UDP",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getPodPorts(tt.args.pod); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getPodPorts() = %v, want %v", got, tt.want)
			}
		})
	}
}
