/*
Copyright 2014 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package noop

import (
	"fmt"
	"os"
	"path"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utiltesting "k8s.io/client-go/util/testing"
	"k8s.io/kubernetes/pkg/volume"
	volumetest "k8s.io/kubernetes/pkg/volume/testing"
	volumeutil "k8s.io/kubernetes/pkg/volume/util"
	"k8s.io/utils/mount"
)

func TestCanSupport(t *testing.T) {
	tmpDir, err := utiltesting.MkTmpdir("noopTest")
	if err != nil {
		t.Fatalf("can't make a temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	plugMgr := volume.VolumePluginMgr{}
	plugMgr.InitPlugins(ProbeVolumePlugins(), nil /* prober */, volumetest.NewFakeVolumeHost(t, tmpDir, nil, nil))
	plug, err := plugMgr.FindPluginByName("kubernetes.io/noop")
	if err != nil {
		t.Errorf("Can't find the plugin by name")
	}
	if plug.GetPluginName() != "kubernetes.io/noop" {
		t.Errorf("Wrong name: %s", plug.GetPluginName())
	}
	if plug.CanSupport(&volume.Spec{Volume: &v1.Volume{VolumeSource: v1.VolumeSource{}}}) {
		t.Errorf("Expected false")
	}

	testCases := map[string]struct {
		volumeSpec *volume.Spec
		exepcted   bool
	}{
		"empty volumesource": {
			volumeSpec: &volume.Spec{Volume: &v1.Volume{VolumeSource: v1.VolumeSource{}}},
			exepcted:   false,
		},
		"cephfs": {
			volumeSpec: &volume.Spec{PersistentVolume: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{volumeutil.VolumePluginNoopAnnotationKey: "true"}},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeSource: v1.PersistentVolumeSource{
						CephFS: &v1.CephFSPersistentVolumeSource{}}}},
			},
			exepcted: true,
		},
		"nfs": {
			volumeSpec: &volume.Spec{PersistentVolume: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{volumeutil.VolumePluginNoopAnnotationKey: "true"}},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeSource: v1.PersistentVolumeSource{
						NFS: &v1.NFSVolumeSource{}}}},
			},
			exepcted: true,
		},
		"bytedrive": {
			volumeSpec: &volume.Spec{PersistentVolume: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{volumeutil.VolumePluginNoopAnnotationKey: "true"}},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeSource: v1.PersistentVolumeSource{
						CSI: &v1.CSIPersistentVolumeSource{Driver: volumeutil.ByteDriveCSIDriverName}}}},
			},
			exepcted: true,
		},
		"zenya": {
			volumeSpec: &volume.Spec{PersistentVolume: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{volumeutil.VolumePluginNoopAnnotationKey: "true"}},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeSource: v1.PersistentVolumeSource{
						CSI: &v1.CSIPersistentVolumeSource{Driver: volumeutil.ZenyaCSIDriverName}}}},
			},
			exepcted: true,
		},
		"other-csi": {
			volumeSpec: &volume.Spec{PersistentVolume: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{volumeutil.VolumePluginNoopAnnotationKey: "true"}},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeSource: v1.PersistentVolumeSource{
						CSI: &v1.CSIPersistentVolumeSource{Driver: "test-driver"}}}},
			},
			exepcted: false,
		},
	}

	for name, tc := range testCases {
		canSupport := plug.CanSupport(tc.volumeSpec)
		if canSupport != tc.exepcted {
			t.Errorf("Volume %s: expected %v, but got %v", name, tc.exepcted, canSupport)
		}
	}
}

func doTestPlugin(t *testing.T, spec *volume.Spec) {
	tmpDir, err := utiltesting.MkTmpdir("noopTest")
	if err != nil {
		t.Fatalf("can't make a temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	plugMgr := volume.VolumePluginMgr{}
	plugMgr.InitPlugins(ProbeVolumePlugins(), nil /* prober */, volumetest.NewFakeVolumeHost(t, tmpDir, nil, nil))
	plug, err := plugMgr.FindPluginByName("kubernetes.io/noop")
	if err != nil {
		t.Errorf("Can't find the plugin by name")
	}

	physicalMounter := mount.FakeMounter{}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID("poduid"),
		},
	}

	wrappedVol, err := plug.(*noopPlugin).constructVolume(spec, pod)
	if err != nil {
		t.Fatalf("Failed to construct wrapped volume: %v", err)
	}

	mounter, err := plug.(*noopPlugin).newMounterInternal(spec, pod, &physicalMounter, wrappedVol, false)
	if err != nil {
		t.Errorf("Failed to make a new Mounter: %v", err)
	}
	if mounter == nil {
		t.Errorf("Got a nil Mounter")
	}
	volumePath := mounter.GetPath()
	volpath := path.Join(tmpDir, fmt.Sprintf("pods/poduid/volumes/kubernetes.io~noop/%s", spec.Name()))
	if volumePath != volpath {
		t.Errorf("Got unexpected path: %s, expected: %s", volumePath, volpath)
	}
	if err := mounter.SetUp(volume.MounterArgs{}); err != nil {
		t.Errorf("Expected success, got: %v", err)
	}
	if _, err := os.Stat(volumePath); err != nil {
		if os.IsNotExist(err) {
			t.Errorf("SetUp() failed, volume path not created: %s", volumePath)
		} else {
			t.Errorf("SetUp() failed: %v", err)
		}
	}
	unmounter, err := plug.(*noopPlugin).newUnmounterInternal(spec.Name(), types.UID("poduid"), &mount.FakeMounter{})
	if err != nil {
		t.Errorf("Failed to make a new Unmounter: %v", err)
	}
	if unmounter == nil {
		t.Errorf("Got a nil Unmounter")
	}
	if err := unmounter.TearDown(); err != nil {
		t.Errorf("Expected success, got: %v", err)
	}
	if _, err := os.Stat(volumePath); err == nil {
		t.Errorf("TearDown() failed, volume path still exists: %s", volumePath)
	} else if !os.IsNotExist(err) {
		t.Errorf("TearDown() failed: %v", err)
	}
}

func TestPlugin(t *testing.T) {
	testCases := map[string]struct {
		volumeSpec *volume.Spec
	}{
		"cephfs": {
			volumeSpec: &volume.Spec{PersistentVolume: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "cephfs",
					Annotations: map[string]string{volumeutil.VolumePluginNoopAnnotationKey: "true"},
				},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeSource: v1.PersistentVolumeSource{
						CephFS: &v1.CephFSPersistentVolumeSource{}}}},
			},
		},
		"nfs": {
			volumeSpec: &volume.Spec{PersistentVolume: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "nfs",
					Annotations: map[string]string{volumeutil.VolumePluginNoopAnnotationKey: "true"},
				},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeSource: v1.PersistentVolumeSource{
						NFS: &v1.NFSVolumeSource{}}}},
			},
		},
		"bytedrive": {
			volumeSpec: &volume.Spec{PersistentVolume: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "bytedrive",
					Annotations: map[string]string{volumeutil.VolumePluginNoopAnnotationKey: "true"},
				},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeSource: v1.PersistentVolumeSource{
						CSI: &v1.CSIPersistentVolumeSource{
							Driver: volumeutil.ByteDriveCSIDriverName,
							VolumeAttributes: map[string]string{
								"clusterName":   "cluster1",
								"regionName":    "region1",
								"bytedriveUUID": "uuid",
							},
						},
					},
				}},
			},
		},
		"zenya": {
			volumeSpec: &volume.Spec{PersistentVolume: &v1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "zenya",
					Annotations: map[string]string{volumeutil.VolumePluginNoopAnnotationKey: "true"},
				},
				Spec: v1.PersistentVolumeSpec{
					PersistentVolumeSource: v1.PersistentVolumeSource{
						CSI: &v1.CSIPersistentVolumeSource{
							Driver: volumeutil.ZenyaCSIDriverName,
							VolumeAttributes: map[string]string{
								"portal": "portal1",
								"token":  "token1",
							},
						},
					},
				}},
			},
		},
	}

	for _, tc := range testCases {
		doTestPlugin(t, tc.volumeSpec)
	}
}

func doTestConstructVolumeSpec(t *testing.T, volumeName string, vol *wrappedVolume) {
	tmpDir, err := utiltesting.MkTmpdir("noopTest")
	if err != nil {
		t.Fatalf("can't make a temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := writeWrappedVolumeSpec(tmpDir, vol); err != nil {
		t.Fatalf("write volume json faile failed: %v", err)
	}

	plugMgr := volume.VolumePluginMgr{}
	plugMgr.InitPlugins(ProbeVolumePlugins(), nil /* prober */, volumetest.NewFakeVolumeHost(t, tmpDir, nil, nil))
	plug, err := plugMgr.FindPluginByName("kubernetes.io/noop")
	if err != nil {
		t.Fatalf("Can't find the plugin by name")
	}

	spec, err := plug.ConstructVolumeSpec(volumeName, tmpDir)
	if err != nil {
		t.Fatalf("construct volume spec failed: %v", err)
	}

	if spec == nil || spec.PersistentVolume == nil {
		t.Fatalf("construct spec pv failed")
	}

	if vol.CephFS != nil && spec.PersistentVolume.Spec.CephFS == nil {
		t.Fatalf("construct %s volume spec pv failed", volumeName)
	}

	if vol.NFS != nil && spec.PersistentVolume.Spec.NFS == nil {
		t.Fatalf("construct %s volume spec pv failed", volumeName)
	}

	if vol.Bytedrive != nil && (spec.PersistentVolume.Spec.CSI == nil || spec.PersistentVolume.Spec.CSI.Driver != volumeutil.ByteDriveCSIDriverName) {
		t.Fatalf("construct %s volume spec pv failed", volumeName)
	}

	if vol.Zenya != nil && (spec.PersistentVolume.Spec.CSI == nil || spec.PersistentVolume.Spec.CSI.Driver != volumeutil.ZenyaCSIDriverName) {
		t.Fatalf("construct %s volume spec pv failed", volumeName)
	}
}

func TestConstructVolumeSpec(t *testing.T) {
	testCases := map[string]*wrappedVolume{
		"cephfs": {
			CephFS: &cephfs{},
		},
		"nfs": {
			NFS: &nfs{},
		},
		"bytedrive": {
			Bytedrive: &bytedriveBlk{},
		},
		"zenya": {
			Zenya: &zenyaBlk{},
		},
	}

	for key, tc := range testCases {
		doTestConstructVolumeSpec(t, key, tc)
	}
}
