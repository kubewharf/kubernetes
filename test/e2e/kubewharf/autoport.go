package kubewharf

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/onsi/ginkgo"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	utilpod "k8s.io/kubernetes/pkg/api/v1/pod"
	"k8s.io/kubernetes/test/e2e/framework"
	e2enode "k8s.io/kubernetes/test/e2e/framework/node"
	imageutils "k8s.io/kubernetes/test/utils/image"
	admissionapi "k8s.io/pod-security-admission/api"
)

const (
	maxNodesToCheck = 10
	maxAutoports    = 5

	pollInterval = 1 * time.Second
	timeout      = time.Second * 30
)

var _ = KubewharfDescribe("Autoport", func() {
	f := framework.NewDefaultFramework("autport")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelPrivileged

	var (
		c  clientset.Interface
		ns string

		nodeNames sets.String
	)

	ginkgo.BeforeEach(func() {
		c = f.ClientSet
		ns = f.Namespace.Name

		nodes, err := e2enode.GetBoundedReadySchedulableNodes(c, maxNodesToCheck)
		framework.ExpectNoError(err)
		nodeNames = sets.NewString()
		for i := 0; i < len(nodes.Items); i++ {
			nodeNames.Insert(nodes.Items[i].Name)
		}
	})

	ginkgo.It("autoport should assign host port", func() {
		podNames := sets.NewString()

		for node := range nodeNames {
			name := fmt.Sprintf("autoport-assign-hostport-%s", uuid.NewUUID())
			pod := getPodWithAutports(name, ns, node, rand.Intn(maxAutoports)+1, false)

			_, err := c.CoreV1().Pods(ns).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err, "creating pod with autport %s in %s")
			podNames.Insert(name)
		}

		errCh := make(chan error, len(podNames))
		for name := range podNames {
			podName := name
			go func() {
				errMsg := ""
				err := wait.Poll(pollInterval, timeout, func() (bool, error) {
					pod, err := c.CoreV1().Pods(ns).Get(context.TODO(), podName, metav1.GetOptions{})
					framework.ExpectNoError(err)

					container := pod.Spec.Containers[0]

					for i, port := range container.Ports {
						autoport := strconv.Itoa(int(port.HostPort))
						if autoport == "0" {
							errMsg = fmt.Sprintf("autoport not assigned for pod %s in %s", podName, ns)
							return false, nil
						}

						if i == 0 {
							if getPortEnv(container.Env) != autoport {
								errMsg = fmt.Sprintf("PORT env not updated for pod %s in %s", podName, ns)
								return false, nil
							}
						}

						if getPortEnvByID(i, container.Env) != autoport {
							errMsg = fmt.Sprintf("PORT%d env not updated for pod %s in %s", i, podName, ns)
							return false, nil
						}
					}

					return true, nil
				})
				if err != nil {
					errCh <- errors.New(errMsg)
				} else {
					errCh <- nil
				}
			}()
		}

		for i := 0; i < len(podNames); i++ {
			err := <-errCh
			framework.ExpectNoError(err)
		}
	})

	ginkgo.It("autoport with host network should assign container and host port", func() {
		podNames := sets.NewString()

		for node := range nodeNames {
			name := fmt.Sprintf("autoport-assign-hostport-%s", uuid.NewUUID())
			pod := getPodWithAutports(name, ns, node, rand.Intn(maxAutoports)+1, true)

			_, err := c.CoreV1().Pods(ns).Create(context.TODO(), pod, metav1.CreateOptions{})
			framework.ExpectNoError(err, "creating pod with autport and hostnetwork %s in %s")
			podNames.Insert(name)
		}

		errCh := make(chan error, len(podNames))
		for name := range podNames {
			podName := name
			go func() {
				errMsg := ""
				err := wait.Poll(pollInterval, timeout, func() (bool, error) {
					pod, err := c.CoreV1().Pods(ns).Get(context.TODO(), podName, metav1.GetOptions{})
					framework.ExpectNoError(err)

					container := pod.Spec.Containers[0]

					for i, port := range container.Ports {
						autoport := strconv.Itoa(int(port.HostPort))
						if autoport == "0" {
							errMsg = fmt.Sprintf("autoport not assigned for pod %s in %s", podName, ns)
							return false, nil
						}

						containerPort := strconv.Itoa(int(port.HostPort))
						if containerPort == "0" {
							errMsg = fmt.Sprintf("containerPort not assigned for pod %s in %s", podName, ns)
							return false, nil
						}

						if autoport != containerPort {
							errMsg = fmt.Sprintf("containerPort and hostPort assigned different values for pod %s in %s", podName, ns)
							return false, nil
						}

						if i == 0 {
							if getPortEnv(container.Env) != autoport {
								errMsg = fmt.Sprintf("PORT env not updated for pod %s in %s", podName, ns)
								return false, nil
							}
						}

						if getPortEnvByID(i, container.Env) != autoport {
							errMsg = fmt.Sprintf("PORT%d env not updated for pod %s in %s", i, podName, ns)
							return false, nil
						}
					}

					return true, nil
				})
				if err != nil {
					errCh <- errors.New(errMsg)
				} else {
					errCh <- nil
				}
			}()
		}

		for i := 0; i < len(podNames); i++ {
			err := <-errCh
			framework.ExpectNoError(err)
		}
	})
})

func getPodWithAutports(name, namespace, node string, numAutoports int, isHostNetwork bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				utilpod.PodAutoPortAnnotation: "1",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{
				{
					Name:  "busybox",
					Image: imageutils.GetPauseImageName(),
				},
			},
		},
	}

	if isHostNetwork {
		pod.Spec.HostNetwork = true
	}

	ports := make([]corev1.ContainerPort, numAutoports)
	for i := 0; i < numAutoports; i++ {
		ports[i] = corev1.ContainerPort{
			Name:          fmt.Sprintf("autoport-%d", i),
			HostPort:      0,
			ContainerPort: 0,
		}

		if !isHostNetwork {
			ports[i].ContainerPort = int32(rand.Intn(65535) + 1)
		}
	}
	pod.Spec.Containers[0].Ports = ports

	return pod
}

func getPortEnv(envs []corev1.EnvVar) string {
	for _, e := range envs {
		if e.Name == "PORT" {
			return e.Value
		}
	}
	return ""
}

func getPortEnvByID(id int, envs []corev1.EnvVar) string {
	for _, e := range envs {
		if e.Name == fmt.Sprintf("PORT%d", id) {
			return e.Value
		}
	}
	return ""
}
