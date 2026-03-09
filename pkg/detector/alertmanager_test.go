/*
Copyright AppsCode Inc. and Contributors

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

package detector

import (
	"context"
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestAlertmanagerDetectorOpenShiftManaged(t *testing.T) {
	tests := []struct {
		name      string
		openShift bool
	}{
		{name: "detects OpenShift cluster", openShift: true},
		{name: "detects non OpenShift cluster", openShift: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapper := newAlertmanagerTestRESTMapper(tt.openShift)
			item := unstructured.Unstructured{}
			item.SetGroupVersionKind(GVKAlertmanager)
			item.SetNamespace("test")
			item.SetName("test")

			kc := &alertmanagerStubClient{
				mapper: mapper,
				items:  []unstructured.Unstructured{item},
			}

			d := NewAlertmanagerDetector(kc)
			ready, err := d.Ready()
			if err != nil {
				t.Fatalf("expected detector to become ready: %v", err)
			}
			if !ready {
				t.Fatalf("expected detector to be ready")
			}

			if got := d.OpenShiftManaged(); got != tt.openShift {
				t.Fatalf("OpenShiftManaged() = %v, want %v", got, tt.openShift)
			}
		})
	}
}

func TestAlertmanagerDetectorOpenShiftManagedPanicsBeforeReady(t *testing.T) {
	mapper := newAlertmanagerTestRESTMapper(true)
	kc := &alertmanagerStubClient{mapper: mapper}

	d := NewAlertmanagerDetector(kc)

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("expected panic when OpenShiftManaged() is called before Ready()")
		}
		if recovered != "NotReady" {
			t.Fatalf("panic = %v, want NotReady", recovered)
		}
	}()

	_ = d.OpenShiftManaged()
}

func newAlertmanagerTestRESTMapper(openShift bool) meta.RESTMapper {
	amGV := schema.GroupVersion{Group: "monitoring.coreos.com", Version: "v1"}
	gvs := []schema.GroupVersion{amGV}
	if openShift {
		gvs = append(gvs, schema.GroupVersion{Group: "project.openshift.io", Version: "v1"})
	}

	mapper := meta.NewDefaultRESTMapper(gvs)
	mapper.AddSpecific(
		schema.GroupVersionKind{Group: amGV.Group, Version: amGV.Version, Kind: "Alertmanager"},
		schema.GroupVersionResource{Group: amGV.Group, Version: amGV.Version, Resource: "alertmanagers"},
		schema.GroupVersionResource{Group: amGV.Group, Version: amGV.Version, Resource: "alertmanager"},
		meta.RESTScopeNamespace,
	)

	if openShift {
		projectGV := schema.GroupVersion{Group: "project.openshift.io", Version: "v1"}
		mapper.AddSpecific(
			schema.GroupVersionKind{Group: projectGV.Group, Version: projectGV.Version, Kind: "Project"},
			schema.GroupVersionResource{Group: projectGV.Group, Version: projectGV.Version, Resource: "projects"},
			schema.GroupVersionResource{Group: projectGV.Group, Version: projectGV.Version, Resource: "project"},
			meta.RESTScopeRoot,
		)
	}

	return mapper
}

type alertmanagerStubClient struct {
	mapper meta.RESTMapper
	items  []unstructured.Unstructured
}

func (s *alertmanagerStubClient) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return fmt.Errorf("not implemented")
}

func (s *alertmanagerStubClient) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	ul, ok := list.(*unstructured.UnstructuredList)
	if !ok {
		return fmt.Errorf("unsupported list type: %T", list)
	}
	ul.Items = append([]unstructured.Unstructured(nil), s.items...)
	return nil
}

func (s *alertmanagerStubClient) Apply(context.Context, runtime.ApplyConfiguration, ...client.ApplyOption) error {
	return fmt.Errorf("not implemented")
}

func (s *alertmanagerStubClient) Create(context.Context, client.Object, ...client.CreateOption) error {
	return fmt.Errorf("not implemented")
}

func (s *alertmanagerStubClient) Delete(context.Context, client.Object, ...client.DeleteOption) error {
	return fmt.Errorf("not implemented")
}

func (s *alertmanagerStubClient) Update(context.Context, client.Object, ...client.UpdateOption) error {
	return fmt.Errorf("not implemented")
}

func (s *alertmanagerStubClient) Patch(context.Context, client.Object, client.Patch, ...client.PatchOption) error {
	return fmt.Errorf("not implemented")
}

func (s *alertmanagerStubClient) DeleteAllOf(context.Context, client.Object, ...client.DeleteAllOfOption) error {
	return fmt.Errorf("not implemented")
}

func (s *alertmanagerStubClient) Status() client.SubResourceWriter {
	return &alertmanagerStubSubResourceClient{}
}

func (s *alertmanagerStubClient) SubResource(string) client.SubResourceClient {
	return &alertmanagerStubSubResourceClient{}
}

func (s *alertmanagerStubClient) Scheme() *runtime.Scheme {
	return runtime.NewScheme()
}

func (s *alertmanagerStubClient) RESTMapper() meta.RESTMapper {
	return s.mapper
}

func (s *alertmanagerStubClient) GroupVersionKindFor(runtime.Object) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, nil
}

func (s *alertmanagerStubClient) IsObjectNamespaced(runtime.Object) (bool, error) {
	return true, nil
}

type alertmanagerStubSubResourceClient struct{}

func (s *alertmanagerStubSubResourceClient) Get(context.Context, client.Object, client.Object, ...client.SubResourceGetOption) error {
	return fmt.Errorf("not implemented")
}

func (s *alertmanagerStubSubResourceClient) Create(context.Context, client.Object, client.Object, ...client.SubResourceCreateOption) error {
	return fmt.Errorf("not implemented")
}

func (s *alertmanagerStubSubResourceClient) Update(context.Context, client.Object, ...client.SubResourceUpdateOption) error {
	return fmt.Errorf("not implemented")
}

func (s *alertmanagerStubSubResourceClient) Patch(context.Context, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
	return fmt.Errorf("not implemented")
}

var _ client.Client = (*alertmanagerStubClient)(nil)
var _ client.SubResourceClient = (*alertmanagerStubSubResourceClient)(nil)
