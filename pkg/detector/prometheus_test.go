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

func TestPrometheusDetectorOpenShiftManaged(t *testing.T) {
	tests := []struct {
		name      string
		openShift bool
	}{
		{
			name:      "detects OpenShift cluster",
			openShift: true,
		},
		{
			name:      "detects non OpenShift cluster",
			openShift: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapper := newTestRESTMapper(tt.openShift)
			item := unstructured.Unstructured{}
			item.SetGroupVersionKind(GVKPrometheus)
			item.SetNamespace("test")
			item.SetName("test")

			kc := &stubClient{
				mapper: mapper,
				items:  []unstructured.Unstructured{item},
			}

			d := NewPrometheusDetector(kc)
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

func TestPrometheusDetectorOpenShiftManagedPanicsBeforeReady(t *testing.T) {
	mapper := newTestRESTMapper(true)
	kc := &stubClient{mapper: mapper}

	d := NewPrometheusDetector(kc)

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

func newTestRESTMapper(openShift bool) meta.RESTMapper {
	promGV := schema.GroupVersion{Group: "monitoring.coreos.com", Version: "v1"}
	gvs := []schema.GroupVersion{promGV}
	if openShift {
		gvs = append(gvs, schema.GroupVersion{Group: "project.openshift.io", Version: "v1"})
	}

	mapper := meta.NewDefaultRESTMapper(gvs)
	mapper.AddSpecific(
		schema.GroupVersionKind{Group: promGV.Group, Version: promGV.Version, Kind: "Prometheus"},
		schema.GroupVersionResource{Group: promGV.Group, Version: promGV.Version, Resource: "prometheuses"},
		schema.GroupVersionResource{Group: promGV.Group, Version: promGV.Version, Resource: "prometheus"},
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

type stubClient struct {
	mapper meta.RESTMapper
	items  []unstructured.Unstructured
}

func (s *stubClient) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return fmt.Errorf("not implemented")
}

func (s *stubClient) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	ul, ok := list.(*unstructured.UnstructuredList)
	if !ok {
		return fmt.Errorf("unsupported list type: %T", list)
	}
	ul.Items = append([]unstructured.Unstructured(nil), s.items...)
	return nil
}

func (s *stubClient) Apply(context.Context, runtime.ApplyConfiguration, ...client.ApplyOption) error {
	return fmt.Errorf("not implemented")
}

func (s *stubClient) Create(context.Context, client.Object, ...client.CreateOption) error {
	return fmt.Errorf("not implemented")
}

func (s *stubClient) Delete(context.Context, client.Object, ...client.DeleteOption) error {
	return fmt.Errorf("not implemented")
}

func (s *stubClient) Update(context.Context, client.Object, ...client.UpdateOption) error {
	return fmt.Errorf("not implemented")
}

func (s *stubClient) Patch(context.Context, client.Object, client.Patch, ...client.PatchOption) error {
	return fmt.Errorf("not implemented")
}

func (s *stubClient) DeleteAllOf(context.Context, client.Object, ...client.DeleteAllOfOption) error {
	return fmt.Errorf("not implemented")
}

func (s *stubClient) Status() client.SubResourceWriter {
	return &stubSubResourceClient{}
}

func (s *stubClient) SubResource(string) client.SubResourceClient {
	return &stubSubResourceClient{}
}

func (s *stubClient) Scheme() *runtime.Scheme {
	return runtime.NewScheme()
}

func (s *stubClient) RESTMapper() meta.RESTMapper {
	return s.mapper
}

func (s *stubClient) GroupVersionKindFor(runtime.Object) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, nil
}

func (s *stubClient) IsObjectNamespaced(runtime.Object) (bool, error) {
	return true, nil
}

type stubSubResourceClient struct{}

func (s *stubSubResourceClient) Get(context.Context, client.Object, client.Object, ...client.SubResourceGetOption) error {
	return fmt.Errorf("not implemented")
}

func (s *stubSubResourceClient) Create(context.Context, client.Object, client.Object, ...client.SubResourceCreateOption) error {
	return fmt.Errorf("not implemented")
}

func (s *stubSubResourceClient) Update(context.Context, client.Object, ...client.SubResourceUpdateOption) error {
	return fmt.Errorf("not implemented")
}

func (s *stubSubResourceClient) Patch(context.Context, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
	return fmt.Errorf("not implemented")
}

var _ client.Client = (*stubClient)(nil)
var _ client.SubResourceClient = (*stubSubResourceClient)(nil)
