/*
Copyright 2019 The Kubernetes Authors.

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

package gci

import (
	"strings"
	"testing"
)

type kubeAPIServeETCDEnv struct {
	KubeHome            string
	ETCDServers         string
	ETCDServersOverride string
	CAKey               string
	CACert              string
	CACertPath          string
	APIServerKey        string
	APIServerCert       string
	APIServerCertPath   string
	APIServerKeyPath    string
	ETCDKey             string
	ETCDCert            string
	StorageBackend      string
	StorageMediaType    string
	CompactionInterval  string
}

func TestServerOverride(t *testing.T) {
	testCases := []struct {
		desc string
		env  kubeAPIServeETCDEnv
		want []string
	}{
		{
			desc: "ETCD-SERVERS is not set - default override",
			want: []string{},
		},
		{
			desc: "ETCD-SERVERS and ETCD_SERVERS_OVERRIDES iare set",
			env: kubeAPIServeETCDEnv{
				ETCDServers:         "ETCDServers",
				ETCDServersOverride: "ETCDServersOverrides",
			},
			want: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			c := newManifestTestCase(t, kubeAPIServerManifestFileName, kubeAPIServerStartFuncName, nil)
			defer c.tearDown()
			tc.env.KubeHome = c.kubeHome

			c.mustInvokeFunc(
				tc.env,
				kubeAPIServerConfigScriptName,
				"etcd.template",
				"testdata/kube-apiserver/base.template",
				"testdata/kube-apiserver/etcd.template",
			)
			c.mustLoadPodFromManifest()

			execArgs := c.pod.Spec.Containers[0].Command[2]
			for _, f := range tc.want {
				if !strings.Contains(execArgs, f) {
					t.Fatalf("Got %q, want it to contain %q", execArgs, f)
				}
			}
		})
	}
}

func TestStorageOptions(t *testing.T) {
	testCases := []struct {
		desc     string
		env      kubeAPIServeETCDEnv
		want     []string
		dontWant []string
	}{
		{
			desc: "storage options are supplied",
			env: kubeAPIServeETCDEnv{
				StorageBackend:     "StorageBackend",
				StorageMediaType:   "StorageMediaType",
				CompactionInterval: "1s",
			},
			want: []string{},
		},
		{
			desc: "storage options not not supplied",
			env:  kubeAPIServeETCDEnv{},
			dontWant: []string{
				"--storage-backend",
				"--storage-media-type",
				"--etcd-compaction-interval",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			c := newManifestTestCase(t, kubeAPIServerManifestFileName, kubeAPIServerStartFuncName, nil)
			defer c.tearDown()
			tc.env.KubeHome = c.kubeHome

			c.mustInvokeFunc(
				tc.env,
				kubeAPIServerConfigScriptName,
				"etcd.template",
				"testdata/kube-apiserver/base.template",
				"testdata/kube-apiserver/etcd.template",
			)
			c.mustLoadPodFromManifest()

			execArgs := c.pod.Spec.Containers[0].Command[2]
			for _, f := range tc.want {
				if !strings.Contains(execArgs, f) {
					t.Fatalf("Got %q, want it to contain %q", execArgs, f)
				}
			}

			for _, f := range tc.dontWant {
				if strings.Contains(execArgs, f) {
					t.Fatalf("Got %q, but it was not expected it to contain %q", execArgs, f)
				}
			}
		})
	}
}

func TestTLSFlags(t *testing.T) {
	testCases := []struct {
		desc string
		env  kubeAPIServeETCDEnv
		want []string
	}{
		{
			desc: "mTLS enabled",
			env: kubeAPIServeETCDEnv{
				CAKey:             "CAKey",
				CACert:            "CACert",
				CACertPath:        "CACertPath",
				APIServerKey:      "APIServerKey",
				APIServerCert:     "APIServerCert",
				ETCDKey:           "ETCDKey",
				ETCDCert:          "ETCDCert",
				ETCDServers:       "https://127.0.0.1:2379",
				APIServerKeyPath:  "APIServerKeyPath",
				APIServerCertPath: "APIServerCertPath",
			},
			want: []string{},
		},
		{
			desc: "mTLS disabled",
			want: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			c := newManifestTestCase(t, kubeAPIServerManifestFileName, kubeAPIServerStartFuncName, nil)
			defer c.tearDown()
			tc.env.KubeHome = c.kubeHome

			c.mustInvokeFunc(
				tc.env,
				kubeAPIServerConfigScriptName,
				"etcd.template",
				"testdata/kube-apiserver/base.template",
				"testdata/kube-apiserver/etcd.template",
			)
			c.mustLoadPodFromManifest()

			execArgs := c.pod.Spec.Containers[0].Command[2]
			for _, f := range tc.want {
				if !strings.Contains(execArgs, f) {
					t.Fatalf("Got %q, want it to contain %q", execArgs, f)
				}
			}
		})
	}
}
