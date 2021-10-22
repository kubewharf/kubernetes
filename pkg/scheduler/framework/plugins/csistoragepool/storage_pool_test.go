package csistoragepool

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"code.byted.org/kubernetes/apis/k8s/csi/v1alpha1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/features"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
	fakelisters "k8s.io/kubernetes/pkg/scheduler/listers/fake"
	schedulernodeinfo "k8s.io/kubernetes/pkg/scheduler/nodeinfo"
)

func TestStoragePoolPredicate(t *testing.T) {

	if err := utilfeature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=true", features.CSIStoragePool)); err != nil {
		t.Fatalf("enable featureGate %s failed: %v", features.CSIStoragePool, err)
	}

	type csiStoragePoolConfig struct {
		storageClassName string
		vgName           string
		capacity         int64
	}

	type podPVCConfig struct {
		storageParameters map[string]string
		storageRequest    int64
		scName            string
	}
	volumeBindMode := storagev1.VolumeBindingWaitForFirstConsumer
	hostName := "host1"

	createFakeSpiListerFunc := func(cs []csiStoragePoolConfig) fakelisters.CSIStoragePoolLister {
		var classes []v1alpha1.CSIStorageByClass
		for _, c := range cs {
			classes = append(classes, v1alpha1.CSIStorageByClass{
				StorageClassName: c.storageClassName,
				CapacityLeftForLocalVolume: &v1.ResourceList{
					v1.ResourceName(c.vgName): *resource.NewQuantity(c.capacity, resource.DecimalSI),
				},
			})
		}
		return fakelisters.CSIStoragePoolLister{
			v1alpha1.CSIStoragePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: hostName,
				},
				Spec:   v1alpha1.CSIStoragePoolSpec{DriverName: "lpv"},
				Status: v1alpha1.CSIStoragePoolStatus{Classes: classes},
			},
		}
	}
	nodeInfo := schedulernodeinfo.NewNodeInfo()
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: hostName}}
	nodeInfo.SetNode(node) //nolint:errcheck

	testCases := []struct {
		name string

		poolConfigs   []csiStoragePoolConfig
		podPVCConfigs []podPVCConfig

		wantStatus *framework.Status
	}{
		// When pod did not require a lvm pvc, always fit
		{
			name:        "Positive fit when pod did not request a lpv volume, when node has NO CSIStoragePool",
			poolConfigs: []csiStoragePoolConfig{{"sc1", "vg1", 300}},
			wantStatus:  nil,
		},
		{
			name:        "Positive fit when pod did not request a lpv volume, when node has CSIStoragePool",
			poolConfigs: []csiStoragePoolConfig{{"sc1", "vg1", 300}},
			wantStatus:  nil,
		},

		// Testing capacity:
		// Fit only when exists a CSIStoragePool which has enough capacity on the node.
		{
			name:        "Positive fit when node has enough capacity on vg1",
			poolConfigs: []csiStoragePoolConfig{{"sc1", "vg1", 300}},
			podPVCConfigs: []podPVCConfig{
				{
					storageParameters: map[string]string{VGNameKey: "vg1"},
					storageRequest:    200,
					scName:            "sc1",
				},
			},
			wantStatus: nil,
		},
		{
			name:        "Negative fit when node has NO CSIStoragePool",
			poolConfigs: nil,
			podPVCConfigs: []podPVCConfig{
				{
					storageParameters: map[string]string{VGNameKey: "vg1"},
					storageRequest:    200,
					scName:            "sc1",
				},
			},

			// TODO: should we return error in this case ?
			wantStatus: framework.NewStatus(framework.Unschedulable, fmt.Sprintf("nothing in the storage pool on %s", hostName)),
		},
		{
			name:        "Negative fit when node CSIStoragePool DO NOT has enough capacity",
			poolConfigs: []csiStoragePoolConfig{{"sc1", "vg1", 199}},
			podPVCConfigs: []podPVCConfig{
				{
					storageParameters: map[string]string{VGNameKey: "vg1"},
					storageRequest:    200,
					scName:            "sc1",
				},
			},
			wantStatus: framework.NewStatus(framework.UnschedulableAndUnresolvable, "no suitable pool on host1 for sc1: 200"),
		},

		// Testing vgName param of StorageClass:
		// When `vgName` is defined, fits only when exits a CSIStoragePool with the same `vgName`.
		// When `vgName` is not defined, fits when exits a CSIStoragePool with the same scName.
		{
			name:        "Positive fit when sc.vgName is defined, and CSIStoragePool has vgName",
			poolConfigs: []csiStoragePoolConfig{{"sc1", "vg1", 300}},
			podPVCConfigs: []podPVCConfig{
				{
					storageParameters: map[string]string{VGNameKey: "vg1"},
					storageRequest:    200,
					scName:            "sc1",
				},
			},
			wantStatus: nil,
		},
		{
			name:        "Positive fit when sc.vgName is not defined, and CSIStoragePool is with proper scName and capacity",
			poolConfigs: []csiStoragePoolConfig{{"sc1", "/dev/sdb", 300}},
			podPVCConfigs: []podPVCConfig{
				{
					storageParameters: map[string]string{},
					storageRequest:    200,
					scName:            "sc1",
				},
			},
			wantStatus: nil,
		},
		// skip this. Fix Me(fandeliang)
		//{
		//	name:        "Negative fit when sc.vgName is defined, but CSIStoragePool do not has matching vgName",
		//	poolConfigs: []csiStoragePoolConfig{{"sc1", "vg1", 300}},
		//	podPVCConfigs: []podPVCConfig{
		//		{
		//			storageParameters: map[string]string{VGNameKey: "vg2"},
		//			storageRequest:    200,
		//			scName:            "sc1",
		//		},
		//	},
		//	wantStatus: framework.NewStatus(framework.UnschedulableAndUnresolvable, "no suitable pool on host1 for sc1/vg2: 200"),
		//},

		// Testing name of StorageClass:
		// Fits only when exits a CSIStoragePool with the same `scName`.
		{
			name:        "Positive fit when node has a CSIStoragePool with required StorageClass",
			poolConfigs: []csiStoragePoolConfig{{"sc1", "/dev/sdb", 300}},
			podPVCConfigs: []podPVCConfig{
				{
					storageParameters: map[string]string{},
					storageRequest:    200,
					scName:            "sc1",
				},
			},
			wantStatus: nil,
		},
		{
			name:        "Negative fit when node do not have a CSIStoragePool with required StorageClass",
			poolConfigs: []csiStoragePoolConfig{{"sc2", "/dev/sdb", 300}},
			podPVCConfigs: []podPVCConfig{
				{
					storageParameters: map[string]string{},
					storageRequest:    200,
					scName:            "sc1",
				},
			},
			wantStatus: framework.NewStatus(framework.UnschedulableAndUnresolvable, "no suitable pool on host1 for sc1: 200"),
		},

		// Support multi pvc with different lpv requirements.
		{
			name: "Positive fit when two pvc lpv request can bose be satisfied",
			poolConfigs: []csiStoragePoolConfig{
				{"sc1", "vg2", 300},
				{"sc2", "/dev/sdb", 1000},
			},
			podPVCConfigs: []podPVCConfig{
				{
					storageParameters: map[string]string{},
					storageRequest:    200,
					scName:            "sc1",
				},
				{
					storageParameters: map[string]string{},
					storageRequest:    500,
					scName:            "sc2",
				},
			},
			wantStatus: nil,
		},
		{
			name: "Negative fit when we hav multi pvc, and totally requires too much.",
			poolConfigs: []csiStoragePoolConfig{
				{"sc1", "vg2", 300},
			},
			podPVCConfigs: []podPVCConfig{
				{
					storageParameters: map[string]string{},
					storageRequest:    200,
					scName:            "sc1",
				},
				{
					storageParameters: map[string]string{},
					storageRequest:    200,
					scName:            "sc1",
				},
			},
			wantStatus: framework.NewStatus(framework.UnschedulableAndUnresolvable, "no suitable pool on host1 for sc1: 200"),
		},
	}

	for i := range testCases {
		test := testCases[i]
		t.Run(test.name, func(t *testing.T) {
			pod := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod1",
					Namespace: "default",
				},
			}

			pvcLister := fakelisters.PersistentVolumeClaimLister{}
			classLister := fakelisters.StorageClassLister{}

			for i, pvcConf := range test.podPVCConfigs {
				pvcName := fmt.Sprintf("PVC_%d", i)
				scName := pvcConf.scName
				pod.Spec.Volumes = append(pod.Spec.Volumes, v1.Volume{
					Name: fmt.Sprintf("test-vol%d", i),
					VolumeSource: v1.VolumeSource{
						PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				})
				pvcLister = append(pvcLister, v1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: pvcName, Namespace: "default"},
					Spec: v1.PersistentVolumeClaimSpec{
						StorageClassName: &scName,
						Resources: v1.ResourceRequirements{
							Limits: nil,
							Requests: v1.ResourceList{
								v1.ResourceStorage: *resource.NewQuantity(pvcConf.storageRequest, resource.DecimalSI),
							},
						},
					},
					Status: v1.PersistentVolumeClaimStatus{
						Phase: v1.ClaimPending,
					},
				})

				classLister = append(classLister, storagev1.StorageClass{
					ObjectMeta:        metav1.ObjectMeta{Name: pvcConf.scName},
					VolumeBindingMode: &volumeBindMode,
					Provisioner:       LocalVolumeProvisioner,
					Parameters:        pvcConf.storageParameters,
				})
			}
			filter := StoragePoolChecker{
				storagePoolLister: createFakeSpiListerFunc(test.poolConfigs),
				pvcLister:         pvcLister,
				classLister:       classLister,
			}

			gotStatus := filter.Filter(context.Background(), nil, pod, nodeInfo)
			if !reflect.DeepEqual(gotStatus, test.wantStatus) {
				t.Errorf("status does not match: %v, want: %v", gotStatus, test.wantStatus)
			}

		})

	}
}
