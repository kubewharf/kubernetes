/*
Copyright 2017 The Kubernetes Authors.

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

package rest

import (
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/apis/example"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/client-go/tools/cache"
)

// TestFillObjectMetaSystemFields validates that system populated fields are set on an object
func TestFillObjectMetaSystemFields(t *testing.T) {
	resource := metav1.ObjectMeta{}
	FillObjectMetaSystemFields(&resource)
	if resource.CreationTimestamp.Time.IsZero() {
		t.Errorf("resource.CreationTimestamp is zero")
	} else if len(resource.UID) == 0 {
		t.Errorf("resource.UID missing")
	}
	if len(resource.UID) == 0 {
		t.Errorf("resource.UID missing")
	}
}

// TestHasObjectMetaSystemFieldValues validates that true is returned if and only if all fields are populated
func TestHasObjectMetaSystemFieldValues(t *testing.T) {
	resource := metav1.ObjectMeta{}
	objMeta, err := meta.Accessor(&resource)
	if err != nil {
		t.Fatal(err)
	}
	if metav1.HasObjectMetaSystemFieldValues(objMeta) {
		t.Errorf("the resource does not have all fields yet populated, but incorrectly reports it does")
	}
	FillObjectMetaSystemFields(&resource)
	if !metav1.HasObjectMetaSystemFieldValues(objMeta) {
		t.Errorf("the resource does have all fields populated, but incorrectly reports it does not")
	}
}

// TestValidNamespace validates that namespace rules are enforced on a resource prior to create or update
func TestValidNamespace(t *testing.T) {
	ctx := genericapirequest.NewDefaultContext()
	namespace, _ := genericapirequest.NamespaceFrom(ctx)
	// TODO: use some genericapiserver type here instead of clientapiv1
	resource := example.Pod{}
	if !ValidNamespace(ctx, &resource.ObjectMeta) {
		t.Fatalf("expected success")
	}
	if namespace != resource.Namespace {
		t.Fatalf("expected resource to have the default namespace assigned during validation")
	}
	resource = example.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "other"}}
	if ValidNamespace(ctx, &resource.ObjectMeta) {
		t.Fatalf("Expected error that resource and context errors do not match because resource has different namespace")
	}
	ctx = genericapirequest.NewContext()
	if ValidNamespace(ctx, &resource.ObjectMeta) {
		t.Fatalf("Expected error that resource and context errors do not match since context has no namespace")
	}

	ctx = genericapirequest.NewContext()
	ns := genericapirequest.NamespaceValue(ctx)
	if ns != "" {
		t.Fatalf("Expected the empty string")
	}
}

// TestFillObjectMetaLastUpdateAnnotation validates that only the lastUpdate annotation is added and no other fields changed
func TestFillObjectMetaLastUpdateAnnotation(t *testing.T) {
	resource := &example.Pod{}
	resourceCopy := resource.DeepCopy()

	FillObjectMetaLastUpdateAnnotation(resourceCopy, schema.GroupVersionKind{})
	if _, ok := resourceCopy.Annotations[cache.LastUpdateAnnotation]; !ok {
		t.Fatalf("expected %s annotation to be added", cache.LastUpdateAnnotation)
	}
	if len(resourceCopy.Annotations) != 1 {
		t.Fatalf("expected only %s annotation to be added", cache.LastUpdateAnnotation)
	}
	resourceCopy.Annotations = nil
	if !reflect.DeepEqual(resource, resourceCopy) {
		t.Fatalf("expected rest of resource to be unchanged")
	}

	resource = examplePod()
	resourceCopy = examplePod().DeepCopy()

	FillObjectMetaLastUpdateAnnotation(resourceCopy, schema.GroupVersionKind{})
	if _, ok := resourceCopy.Annotations[cache.LastUpdateAnnotation]; !ok {
		t.Fatalf("expected %s annotation to be added", cache.LastUpdateAnnotation)
	}
	delete(resourceCopy.Annotations, cache.LastUpdateAnnotation)
	if !reflect.DeepEqual(resource, resourceCopy) {
		t.Fatalf("expected rest of resource to be unchanged")
	}
}

func examplePod() *example.Pod {
	t, _ := time.Parse("2022-02-23:53:32", time.RFC3339)
	return &example.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "foo",
			Namespace:         "other",
			UID:               "979a4c94-4af4-4632-8370-8b5e1612e0b6",
			ResourceVersion:   "51112",
			Generation:        6,
			CreationTimestamp: metav1.NewTime(t),
			Labels:            map[string]string{"foo": "bar"},
			Annotations:       map[string]string{"foo": "bar"},
			OwnerReferences:   []metav1.OwnerReference{{Kind: "Deployment", Name: "foobar", UID: "873b4d04-4zp4-1222-8370-1l1t1623e0z8"}},
		},
		Spec: example.PodSpec{
			RestartPolicy: "Always",
			NodeName:      "bar",
			Hostname:      "foobar",
		},
		Status: example.PodStatus{
			Phase:  "Pending",
			HostIP: "0.0.0.0",
			PodIP:  "10.0.0.1",
		},
	}

}
