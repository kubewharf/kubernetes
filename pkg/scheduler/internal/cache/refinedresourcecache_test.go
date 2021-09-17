package cache

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	podutil "k8s.io/kubernetes/pkg/api/v1/pod"
)

func TestShouldDeployVictimsBeThrottled(t *testing.T) {
	tests := []struct {
		name           string
		throttleVal    int64
		expectedResult bool
		annotations    map[string]string
	}{
		{
			name:           "using scheduler config, throttle",
			throttleVal:    3,
			expectedResult: true,
		},
		{
			name:           "using scheduler config, not throttle",
			throttleVal:    4,
			expectedResult: false,
		},
		{
			name:           "using deploy config value, throttle",
			throttleVal:    4,
			expectedResult: true,
			annotations: map[string]string{
				podutil.PreemptThrottleValueKey: "3",
			},
		},
		{
			name:           "using deploy config value, not throttle",
			throttleVal:    3,
			expectedResult: false,
			annotations: map[string]string{
				podutil.PreemptThrottleValueKey: "4",
			},
		},
		{
			name:           "using deploy config percentage, throttle",
			throttleVal:    3,
			expectedResult: true,
			annotations: map[string]string{
				podutil.PreemptThrottleValueKey:      "4",
				podutil.PreemptThrottlePercentageKey: "75",
			},
		},
		{
			name:           "using deploy config percentage, not throttle",
			throttleVal:    3,
			expectedResult: false,
			annotations: map[string]string{
				podutil.PreemptThrottleValueKey:      "4",
				podutil.PreemptThrottlePercentageKey: "100",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pod := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "pod",
					Labels: map[string]string{
						"name": "dp",
					},
				},
			}

			replicas := new(int32)
			*replicas = 4
			deploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "default",
					Name:        "dp",
					Annotations: test.annotations,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: replicas,
				},
			}

			cache := &schedulerCache{
				deployVictims: map[string]victimSet{
					"dp": {
						"pod-1": time.Now(),
						"pod-2": time.Now(),
						"pod-3": time.Now(),
					},
				},
				deployItems: make(map[string]DeployItem),
			}

			cache.SetDeployItems(deploy)
			gotResult := cache.ShouldDeployVictimsBeThrottled(pod, test.throttleVal)
			if gotResult != test.expectedResult {
				t.Errorf("Expected: %v, Got: %v", test.expectedResult, gotResult)
			}
		})
	}
}
