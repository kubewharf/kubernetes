package v1alpha1

import (
	schedulingalpha1 "k8s.io/api/scheduling/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
)

func addDefaultingFuncs(scheme *runtime.Scheme) error {
	return RegisterDefaults(scheme)
}

func SetDefaults_PriorityClass(obj *schedulingalpha1.PriorityClass) {
	if obj.CanBePreempted == nil {
		t := false
		obj.CanBePreempted = &t
	}
}
