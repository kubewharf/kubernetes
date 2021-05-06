package csistoragepool

import (
	"context"
	"fmt"

	"code.byted.org/kubernetes/apis/k8s/csi/v1alpha1"
	csilisters "code.byted.org/kubernetes/clientsets/k8s/listers/csi/v1alpha1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	corelisters "k8s.io/client-go/listers/core/v1"
	storagelisters "k8s.io/client-go/listers/storage/v1"
	"k8s.io/klog"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"k8s.io/kubernetes/pkg/features"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"
	schedulernodeinfo "k8s.io/kubernetes/pkg/scheduler/nodeinfo"
)

const (
	VGNameKey              = "vgname"
	LocalVolumeProvisioner = "local-volume-provisioner"
)

// Name is the name of the plugin used in the plugin registry and configurations.
const Name = "PodFitsStoragePool"

func newPredicateFailureError(name, msg string) error {
	return fmt.Errorf("Predicate %s failed, %s", name, msg)
}

var _ framework.FilterPlugin = &StoragePoolChecker{}

// StoragePoolChecker contains information to check storage pool.
type StoragePoolChecker struct {
	storagePoolLister csilisters.CSIStoragePoolLister
	pvcLister         corelisters.PersistentVolumeClaimLister
	classLister       storagelisters.StorageClassLister
}

func New(_ *runtime.Unknown, handle framework.FrameworkHandle) (framework.Plugin, error) {
	c := &StoragePoolChecker{
		storagePoolLister: handle.BytedInformerFactory().Csi().V1alpha1().CSIStoragePools().Lister(),
		pvcLister:         handle.SharedInformerFactory().Core().V1().PersistentVolumeClaims().Lister(),
		classLister:       handle.SharedInformerFactory().Storage().V1().StorageClasses().Lister(),
	}
	return c, nil
}

func (spc *StoragePoolChecker) Name() string {
	return Name
}

func (spc *StoragePoolChecker) Filter(ctx context.Context, cycleState *framework.CycleState, pod *v1.Pod, nodeInfo *schedulernodeinfo.NodeInfo) *framework.Status {
	// if feature gate is disable we return
	if !utilfeature.DefaultFeatureGate.Enabled(features.CSIStoragePool) {
		return nil
	}
	// If a pod doesn't have any volume attached to it, the predicate will always be true.
	// Thus we make a fast path for it, to avoid unnecessary computations in this case.
	if len(pod.Spec.Volumes) == 0 {
		return nil
	}

	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	var requests = make([]pvcLocalStorageRequest, 0)
	failReasons := []string{}

	namespace := pod.Namespace
	manifest := &(pod.Spec)
	for i := range manifest.Volumes {
		volume := &manifest.Volumes[i]
		if volume.PersistentVolumeClaim != nil {
			pvcName := volume.PersistentVolumeClaim.ClaimName
			if pvcName == "" {
				failReasons = append(failReasons, "PersistentVolumeClaim had no name")
				continue
			}
			pvc, err := spc.pvcLister.PersistentVolumeClaims(namespace).Get(pvcName)
			if err != nil {
				failReasons = append(failReasons, "PersistentVolumeClaim %s/%s not found", namespace, pvcName)
				continue
			}

			if pvc == nil {
				failReasons = append(failReasons, fmt.Sprintf("PersistentVolumeClaim was not found: %q", pvcName))
				continue
			}

			if pvc.Status.Phase != v1.ClaimPending {
				// pvc status is not pending, skip
				continue
			}

			pvName := pvc.Spec.VolumeName
			if len(pvName) > 0 {
				// pv name is set for pvc, skip this predicate
				continue
			}

			// only checks for local storage provisioner
			scName := v1helper.GetPersistentVolumeClaimClass(pvc)
			if len(scName) == 0 {
				// sc name length is 0, do not need to check this here, skip
				continue
			}
			class, err := spc.classLister.Get(scName)
			if err != nil {
				failReasons = append(failReasons, fmt.Sprintf("storageclass was not found: %q", scName))
				continue
			}
			if class != nil {
				if class.VolumeBindingMode == nil {
					failReasons = append(failReasons, fmt.Sprintf("VolumeBindingMode not set for StorageClass: %q", scName))
					continue
				}
				if *class.VolumeBindingMode != storagev1.VolumeBindingWaitForFirstConsumer {
					// VolumeBindingMode is not WaitForFirstConsumer, skip
					continue
				}
				if class.Provisioner != LocalVolumeProvisioner {
					// provisioner is not local storage provisioner, skip
					continue
				}

				// if the pvc is new, add pvc request to pod total request
				// TODO: duplicated calculating may occur here, which may cause wrong predicate result
				// find a way to cache local PVCs/Nodes mapping info.
				requests = append(requests, pvcLocalStorageRequest{
					scName:  class.Name,
					request: pvc.Spec.Resources.Requests[v1.ResourceStorage],
				})
			}
		}
	}

	if len(requests) == 0 {
		return nil
	}

	storagePool, err := spc.storagePoolLister.Get(node.Name)
	if err != nil {
		failReasons = append(failReasons, fmt.Sprintf("no storage pool on %s", node.Name))
		return framework.NewStatus(framework.Error, failReasons...)
	}
	if storagePool == nil || len(storagePool.Status.Classes) == 0 {
		failReasons = append(failReasons, fmt.Sprintf("nothing in the storage pool on %s", node.Name))
		return framework.NewStatus(framework.Error, failReasons...)
	}

	localStoragePools := newLocalStoragePoolInfoList(storagePool.Status.Classes)

	for _, request := range requests {
		if chosen := localStoragePools.choosePoolAndMinus(request); chosen == nil {
			failReasons = append(failReasons, fmt.Sprintf("no suitable pool on %s for %s", node.Name, request))
		}
	}

	if len(failReasons) > 0 {
		return framework.NewStatus(framework.UnschedulableAndUnresolvable, failReasons...)
	}

	klog.Infof("Provisioning for claims of pod %s, on %s with StoragePool support", pod.Name, node.Name)
	return nil
}

type pvcLocalStorageRequest struct {
	scName  string
	request resource.Quantity
}

func (r pvcLocalStorageRequest) String() string {
	return fmt.Sprintf("%s: %s", r.scName, r.request.String())
}

type localStoragePoolInfo struct {
	scName   string
	vgName   v1.ResourceName
	capacity resource.Quantity
}

func (lsp localStoragePoolInfo) canSatisfy(request pvcLocalStorageRequest) bool {
	return lsp.scName == request.scName &&
		lsp.capacity.Cmp(request.request) > 0
}

type localStoragePoolInfoList []*localStoragePoolInfo

func newLocalStoragePoolInfoList(classes []v1alpha1.CSIStorageByClass) localStoragePoolInfoList {
	localStoragePools := make([]*localStoragePoolInfo, 0)
	for _, c := range classes {
		if c.CapacityLeftForLocalVolume != nil {
			for name, allocatableInVG := range *c.CapacityLeftForLocalVolume {
				localStoragePools = append(localStoragePools, &localStoragePoolInfo{
					scName:   c.StorageClassName,
					vgName:   name,
					capacity: allocatableInVG,
				})
			}
		}
	}

	return localStoragePoolInfoList(localStoragePools)
}

func (poolList localStoragePoolInfoList) choosePoolAndMinus(request pvcLocalStorageRequest) *localStoragePoolInfo {
	for _, pool := range poolList {
		if pool.canSatisfy(request) {
			pool.capacity.Sub(request.request)
			return pool
		}
	}
	return nil
}
