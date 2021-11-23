/*
Copyright AppsCode Inc. and Contributors.

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

package embeddedashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	uiv1alpha1 "go.openviz.dev/grafana-tools/apis/ui/v1alpha1"
	gc "go.openviz.dev/grafana-tools/client/clientset/versioned"

	"github.com/grafana-tools/sdk"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Storage struct {
	kc        client.Client
	a         authorizer.Authorizer
	gc        gc.Interface
	convertor rest.TableConvertor
}

var _ rest.GroupVersionKindProvider = &Storage{}
var _ rest.Scoper = &Storage{}
var _ rest.Creater = &Storage{}

//var _ rest.Getter = &Storage{}
//var _ rest.Lister = &Storage{}

func NewStorage(kc client.Client, a authorizer.Authorizer, gc gc.Interface) *Storage {
	return &Storage{
		kc: kc,
		a:  a,
		gc: gc,
		convertor: rest.NewDefaultTableConvertor(schema.GroupResource{
			Group:    "ui.openviz.dev",
			Resource: uiv1alpha1.ResourceEmbeddedDashboards,
		}),
	}
}

const (
	GrafanaHost = "https://grafana.byte.builders"
)

func (r *Storage) GroupVersionKind(_ schema.GroupVersion) schema.GroupVersionKind {
	return uiv1alpha1.SchemeGroupVersion.WithKind(uiv1alpha1.ResourceKindEmbeddedDashboard)
}

func (r *Storage) NamespaceScoped() bool {
	return true
}

func (r *Storage) New() runtime.Object {
	return &uiv1alpha1.EmbeddedDashboard{}
}

func (r *Storage) Create(ctx context.Context, obj runtime.Object, _ rest.ValidateObjectFunc, _ *metav1.CreateOptions) (runtime.Object, error) {
	user, ok := request.UserFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("missing user info")
	}
	req := obj.(*uiv1alpha1.EmbeddedDashboard)

	fmt.Println(user)

	dashboard, err := r.gc.OpenvizV1alpha1().Dashboards(req.Namespace).Get(ctx, req.Spec.Dashboard, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	board := &sdk.Board{}
	if dashboard.Spec.Model != nil {
		err = json.Unmarshal(dashboard.Spec.Model.Raw, board)
		if err != nil {
			return nil, err
		}
	}

	for _, p := range board.Panels {
		// url template: "http://{{.URL}}/d-solo/{{.BoardUID}}/{{.DashboardName}}?orgId={{.OrgID}}&from={{.From}}&to={{.To}}&theme={{.Theme}}&panelId="

		url := fmt.Sprintf("%v/d-solo/%v/%v?orgId=%v&from=%v&to=%v&panelId=%v", GrafanaHost, board.UID, *dashboard.Status.Dashboard.Slug, *dashboard.Status.Dashboard.OrgID, time.Now().Unix(), time.Now().Unix(), p.ID)
		panelURL := uiv1alpha1.PanelURL{
			Title:       p.Title,
			EmbeddedURL: url,
		}
		req.Spec.URLs = append(req.Spec.URLs, panelURL)
	}
	fmt.Println(req.Spec.URLs)

	return req, nil
}

//func (r *Storage) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
//	ns, ok := request.NamespaceFrom(ctx)
//	if !ok {
//		return nil, apierrors.NewBadRequest("missing namespace")
//	}
//	user, ok := request.UserFrom(ctx)
//	if !ok {
//		return nil, apierrors.NewBadRequest("missing user info")
//	}
//	fmt.Println(user)
//
//	emDash := &uiv1alpha1.EmbeddedDashboard{}
//	err := r.kc.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, emDash)
//	if err != nil {
//		return nil, apierrors.NewInternalError(err)
//	}
//	return emDash, nil
//}
//
//func (r *Storage) NewList() runtime.Object {
//	return &uiv1alpha1.EmbeddedDashboardList{}
//}
//
//func (r *Storage) List(ctx context.Context, options *internalversion.ListOptions) (runtime.Object, error) {
//	ns, ok := request.NamespaceFrom(ctx)
//	if !ok {
//		return nil, apierrors.NewBadRequest("missing namespace")
//	}
//
//	user, ok := request.UserFrom(ctx)
//	if !ok {
//		return nil, apierrors.NewBadRequest("missing user info")
//	}
//	fmt.Println(user)
//	opts := client.ListOptions{Namespace: ns}
//	if options != nil {
//		opts.LabelSelector = options.LabelSelector
//		opts.FieldSelector = options.FieldSelector
//		opts.Limit = options.Limit
//		opts.Continue = options.Continue
//	}
//
//	var emDashList uiv1alpha1.EmbeddedDashboardList
//	err := r.kc.List(ctx, &emDashList, &opts)
//	if err != nil {
//		return nil, err
//	}
//	return &emDashList, nil
//}
//
//func (r *Storage) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
//	return r.convertor.ConvertToTable(ctx, object, tableOptions)
//}
