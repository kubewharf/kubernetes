package topologymanager

import (
	"fmt"
	"strconv"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	"k8s.io/kubernetes/pkg/kubelet/cm/topologymanager/bitmask"
	"k8s.io/kubernetes/staging/src/k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/staging/src/k8s.io/client-go/util/retry"
)

const (
	AepCsiStorageClass           = "aep-storage-class"
	AllocateNUMAByTopologyManger = "tce.kubernetes.io/pv-allocate-by-topology"
	LVPath                       = "LVPath"
	LVNUMA                       = "NUMAID"
	AepCSI                       = "aep-csi"
)

type AepCSIHintProvider struct {
	kubeClient clientset.Interface
	// numa bit on this node
	numaBits              []int
	topologyAffinityStore Store
}

func NewAepCSIHintProvider(kubeClient clientset.Interface, numaBits []int, topologyAffinityStore Store) *AepCSIHintProvider {
	klog.Infof("[aep-csi-hint-provider] numa set is %v", numaBits)
	return &AepCSIHintProvider{
		numaBits:              numaBits,
		kubeClient:            kubeClient,
		topologyAffinityStore: topologyAffinityStore,
	}
}

func (aepCSIHintProvider *AepCSIHintProvider) GetTopologyHints(pod *v1.Pod, container *v1.Container) map[string][]TopologyHint {
	// container numa request is zero, return
	socketLimit := container.Resources.Limits[v1.ResourceBytedanceSocket]
	if socketLimit.Value() == 0 {
		return nil
	}
	// pod with out aep request skip
	aepLimit := container.Resources.Limits[v1.ResourceBytedanceAEP]
	if aepLimit.Value() == 0 {
		return nil
	}
	// pod with out annotation skip
	if len(pod.Annotations[AllocateNUMAByTopologyManger]) == 0 {
		return nil
	}
	rejectedHints := map[string][]TopologyHint{
		AepCSI: []TopologyHint{},
	}
	requiredNuma := 0
	// numa id set of allocated aep device
	allocatedNumas := sets.NewInt()
	pvs := sets.NewString()
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil && len(volume.PersistentVolumeClaim.ClaimName) > 0 {
			// get pvc from apiserver
			pvc, err := aepCSIHintProvider.kubeClient.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(volume.PersistentVolumeClaim.ClaimName, metav1.GetOptions{ResourceVersion: "0"})
			if err != nil {
				klog.Errorf("get pvc %s failed, err is %s", volume.PersistentVolumeClaim.ClaimName, err.Error())
				return rejectedHints
			}
			// find pvc with storage class aepCSIHintProvider
			if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != AepCsiStorageClass {
				continue
			}
			if len(pvc.Spec.VolumeName) == 0 {
				// pvc should be bounded when pod has been scheduled to node
				klog.Error("pvc %s is not bounded to pv", pvc.Name)
				return rejectedHints
			}
			klog.Infof("[aep-csi-hint-provider] pod %s container %s pvc name is %s, bounded pv is %s", pod.Name, container.Name, pvc.Name, pvc.Spec.VolumeName)
			// get pv from apiserver
			pv, err := aepCSIHintProvider.kubeClient.CoreV1().PersistentVolumes().Get(pvc.Spec.VolumeName, metav1.GetOptions{})
			if err != nil {
				klog.Error("get pv %s for pvc %s failed, err is %s", pvc.Spec.VolumeName, pvc.Name, err.Error())
				return rejectedHints
			}
			// if csi lvpath and num is empty, should allocate numa
			if len(pv.Spec.CSI.VolumeAttributes[LVPath]) == 0 {
				requiredNuma += 1
				klog.Infof("[aep-csi-hint-provider] pod %s container %s pvc name pv %s lv path not set", pod.Name, container.Name, pv.Name)
				if len(pv.Spec.CSI.VolumeAttributes[LVNUMA]) == 0 {
					klog.Infof("[aep-csi-hint-provider] pv %s lv numa id not set", pv.Name)
					pvs.Insert(pv.Name)
				} else if id, err := strconv.Atoi(pv.Spec.CSI.VolumeAttributes[LVNUMA]); err == nil && id < len(aepCSIHintProvider.numaBits) {
					klog.Infof("[aep-csi-hint-provider] pv %s lv numa id already set %s", pv.Name, pv.Spec.CSI.VolumeAttributes[LVNUMA])
					allocatedNumas.Insert(id)
				} else {
					klog.Error("pv %s for pvc %s allcoated numa id is %s, butmachine only has %d numas", pvc.Spec.VolumeName, pvc.Name, pv.Spec.CSI.VolumeAttributes[LVNUMA], len(aepCSIHintProvider.numaBits))
					return rejectedHints
				}
			}
		}
	}
	klog.Infof("[aep-csi-hint-provider] pod %s container %s aep with numa required is %d, allocated is %v, socket required is %d",
		pod.Name, container.Name, requiredNuma, allocatedNumas.List(), socketLimit.Value())
	// no aep device should be allocated
	if requiredNuma == 0 {
		return nil
	}
	// bit count in hint must be equatl to numa requirement in limit
	if int64(requiredNuma) > socketLimit.Value() {
		klog.Error("aep device number %d not equal to numa number %s", requiredNuma, socketLimit.Value())
		return rejectedHints
	}
	// some device allocate while others not
	if len(allocatedNumas) > 0 && len(allocatedNumas) != requiredNuma {
		klog.Error()
		return rejectedHints
	}

	if int64(requiredNuma) < socketLimit.Value() {
		requiredNuma = int(socketLimit.Value())
	}

	var result []TopologyHint
	bitmask.IterateBitMasks(aepCSIHintProvider.numaBits, func(b bitmask.BitMask) {
		// allocate aep device numas must be included
		for bit := range allocatedNumas {
			if !b.IsSet(bit) {
				return
			}
		}
		// for not allocated aep device numas, leave topology to decide.
		// the result will be applied to csi numa attribute in allocate phase
		if b.Count() == requiredNuma {
			result = append(result, TopologyHint{NUMANodeAffinity: b, Preferred: true})
		} else if b.Count() > requiredNuma {
			result = append(result, TopologyHint{NUMANodeAffinity: b, Preferred: false})
		}
	})
	return map[string][]TopologyHint{
		AepCSI: result,
	}
}

func (aepCSIHintProvider *AepCSIHintProvider) Allocate(pod *v1.Pod, container *v1.Container) error {
	socketLimit := container.Resources.Limits[v1.ResourceBytedanceSocket]
	if socketLimit.Value() == 0 {
		return nil
	}
	// pod with out aep request skip
	aepLimit := container.Resources.Limits[v1.ResourceBytedanceAEP]
	if aepLimit.Value() == 0 {
		return nil
	}
	// pod with out annotation skip
	if len(pod.Annotations[AllocateNUMAByTopologyManger]) == 0 {
		return nil
	}
	// get hint conclusion from topology store
	hint := aepCSIHintProvider.topologyAffinityStore.GetAffinity(string(pod.UID), container.Name)
	affinity := hint.NUMANodeAffinity
	klog.Infof("[aep-csi-hint-provider] hint provided by topology manager is %v", hint)
	var aepPvs [] *v1.PersistentVolume
	var allErrs []error

	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil && len(volume.PersistentVolumeClaim.ClaimName) > 0 {
			pvc, err := aepCSIHintProvider.kubeClient.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(volume.PersistentVolumeClaim.ClaimName, metav1.GetOptions{ResourceVersion: "0"})
			if err != nil {
				allErrs = append(allErrs, err)
				continue
			}
			if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != AepCsiStorageClass {
				continue
			}
			if len(pvc.Spec.VolumeName) == 0 {
				allErrs = append(allErrs, fmt.Errorf("pvc %s has no volume name", pvc.Name))
				continue
			}
			pv, err := aepCSIHintProvider.kubeClient.CoreV1().PersistentVolumes().Get(pvc.Spec.VolumeName, metav1.GetOptions{})
			if err != nil || pv.Spec.CSI == nil {
				allErrs = append(allErrs, fmt.Errorf("pvc %s has no volume name", pvc.Name))
				continue
			}
			if len(pv.Annotations[AllocateNUMAByTopologyManger]) == 0 {
				continue
			}
			if len(pv.Spec.CSI.VolumeAttributes[LVPath]) == 0 && len(pv.Spec.CSI.VolumeAttributes[LVNUMA]) == 0 {
				aepPvs = append(aepPvs, pv)
			}
		}
	}
	// log aep pv names to bound
	var aepPvNames []string
	for _, pv := range aepPvs {
		aepPvNames = append(aepPvNames, pv.Name)
	}
	klog.Infof("[aep-csi-hint-provider] allocate phase, pv needed to allocate numa is %v", aepPvNames)

	if len(allErrs) != 0 {
		klog.Error("[aep-csi-hint-provider] allocate error is ", utilerrors.NewAggregate(allErrs).Error())
		return utilerrors.NewAggregate(allErrs)
	}
	if len(aepPvs) > hint.NUMANodeAffinity.Count() {
		return fmt.Errorf("aep pv number %d is not equal to hint count %d", len(aepPvs), hint.NUMANodeAffinity.Count())
	}
	// update aep pv csi numaid attribute
	for i, pv := range aepPvs {
		pv.Spec.CSI.VolumeAttributes[LVNUMA] = strconv.Itoa(affinity.GetBits()[i])
		klog.Infof("[aep-csi-hint-provider] dispatch pv %s to numa id %d", pv.Name, affinity.GetBits()[i])
		err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			_, updateErr := aepCSIHintProvider.kubeClient.CoreV1().PersistentVolumes().Update(pv)
			if updateErr != nil {
				updatedPv, getErr := aepCSIHintProvider.kubeClient.CoreV1().PersistentVolumes().Get(pv.Name, metav1.GetOptions{})
				if getErr == nil {
					pv = updatedPv.DeepCopy()
					pv.Spec.CSI.VolumeAttributes[LVNUMA] = strconv.Itoa(affinity.GetBits()[i])
				}
			}
			return updateErr
		})
		if err != nil {
			klog.Errorf("[aep-csi-hint-provider] update pv %s to  numa id %s failed, err is %s", pv.Name, affinity.GetBits()[i], err.Error())
			return err
		}
	}
	return nil
}
