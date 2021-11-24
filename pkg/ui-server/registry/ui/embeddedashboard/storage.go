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

	openvizv1alpha1 "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	uiv1alpha1 "go.openviz.dev/grafana-tools/apis/ui/v1alpha1"

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
	kc client.Client
	a  authorizer.Authorizer
}

var _ rest.GroupVersionKindProvider = &Storage{}
var _ rest.Scoper = &Storage{}
var _ rest.Creater = &Storage{}

func NewStorage(kc client.Client, a authorizer.Authorizer) *Storage {
	return &Storage{
		kc: kc,
		a:  a,
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
	ns, ok := request.NamespaceFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("missing namespace")
	}
	user, ok := request.UserFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("missing user info")
	}
	req := obj.(*uiv1alpha1.EmbeddedDashboard)

	// todo: authorizer need to be added
	fmt.Println(user)

	dashboard := &openvizv1alpha1.Dashboard{}
	err := r.kc.Get(ctx, client.ObjectKey{Namespace: ns, Name: req.Spec.Dashboard}, dashboard)
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
		if p.Type == "row" {
			continue
		}
		// url template: "http://{{.URL}}/d-solo/{{.BoardUID}}/{{.DashboardName}}?orgId={{.OrgID}}&from={{.From}}&to={{.To}}&theme={{.Theme}}&panelId="
		url := fmt.Sprintf("%v/d-solo/%v/%v?orgId=%v&from=%v&to=%v&panelId=%v", GrafanaHost, board.UID, *dashboard.Status.Dashboard.Slug, *dashboard.Status.Dashboard.OrgID, time.Now().Unix(), time.Now().Unix(), p.ID)
		panelURL := uiv1alpha1.PanelURL{
			Title:       p.Title,
			EmbeddedURL: url,
		}
		req.Spec.URLs = append(req.Spec.URLs, panelURL)
	}

	return req, nil
}
