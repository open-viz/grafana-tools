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
	"errors"
	"fmt"
	"time"

	openvizapi "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	uiapi "go.openviz.dev/grafana-tools/apis/ui/v1alpha1"

	"github.com/grafana-tools/sdk"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	appcatalogapi "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Storage struct {
	kc client.Client
	a  authorizer.Authorizer
	gr schema.GroupResource
}

var _ rest.GroupVersionKindProvider = &Storage{}
var _ rest.Scoper = &Storage{}
var _ rest.Creater = &Storage{}

func NewStorage(kc client.Client, a authorizer.Authorizer) *Storage {
	return &Storage{
		kc: kc,
		a:  a,
		gr: openvizapi.Resource(openvizapi.ResourceDashboards),
	}
}

func (r *Storage) GroupVersionKind(_ schema.GroupVersion) schema.GroupVersionKind {
	return uiapi.SchemeGroupVersion.WithKind(uiapi.ResourceKindEmbeddedDashboard)
}

func (r *Storage) NamespaceScoped() bool {
	return true
}

func (r *Storage) New() runtime.Object {
	return &uiapi.EmbeddedDashboard{}
}

func (r *Storage) Create(ctx context.Context, obj runtime.Object, _ rest.ValidateObjectFunc, _ *metav1.CreateOptions) (runtime.Object, error) {
	ns, ok := apirequest.NamespaceFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("missing namespace")
	}

	in := obj.(*uiapi.EmbeddedDashboard)
	if in.Request == nil {
		return nil, apierrors.NewBadRequest("missing apirequest")
	}

	var dashboard openvizapi.Dashboard
	if in.Request.Ref.Name != nil {
		err := r.kc.Get(ctx, client.ObjectKey{Namespace: ns, Name: *in.Request.Ref.Name}, &dashboard)
		if err != nil {
			return nil, err
		}
	} else if in.Request.Ref.Selector != nil {
		var dashboardList openvizapi.DashboardList
		if err := r.kc.List(ctx, &dashboardList, client.InNamespace(ns), client.MatchingFields{
			openvizapi.GrafanaNameKey:    in.Request.Ref.Selector.GrafanaName,
			openvizapi.DashboardTitleKey: in.Request.Ref.Selector.DashboardTitle,
		}); err != nil {
			return nil, err
		}
		if len(dashboardList.Items) == 0 {
			return nil, apierrors.NewNotFound(openvizapi.Resource(openvizapi.ResourceDashboards), fmt.Sprintf("%+v", in.Request.Ref.Selector))
		} else if len(dashboardList.Items) > 1 {
			return nil, apierrors.NewBadRequest(fmt.Sprintf("%+v selects multiple dashboards", in.Request.Ref.Selector))
		}
		dashboard = dashboardList.Items[0]
	}

	user, ok := apirequest.UserFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("missing user info")
	}

	attrs := authorizer.AttributesRecord{
		User:      user,
		Verb:      "get",
		Namespace: ns,
		APIGroup:  r.gr.Group,
		Resource:  r.gr.Resource,
		Name:      dashboard.Name,
	}
	decision, why, err := r.a.Authorize(ctx, attrs)
	if err != nil {
		return nil, apierrors.NewInternalError(err)
	}
	if decision != authorizer.DecisionAllow {
		return nil, apierrors.NewForbidden(r.gr, dashboard.Name, errors.New(why))
	}

	if dashboard.Spec.Grafana == nil {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("Dashboard %s/%s is missing a Grafana ref", dashboard.Namespace, dashboard.Name))
	}
	var ab appcatalogapi.AppBinding
	abKey := client.ObjectKey{Namespace: dashboard.Namespace, Name: dashboard.Spec.Grafana.Name}
	err = r.kc.Get(ctx, abKey, &ab)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch AppBinding %s, reason: %v", abKey, err)
	}
	grafanaHost, err := ab.URL()
	if err != nil {
		return nil, err
	}

	if dashboard.Spec.Model == nil {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("Dashboard %s/%s is missing a model", dashboard.Namespace, dashboard.Name))
	}
	board := &sdk.Board{}
	err = json.Unmarshal(dashboard.Spec.Model.Raw, board)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal model for Dashboard %s/%s, reason: %v", dashboard.Namespace, dashboard.Name, err)
	}

	in.Response = &uiapi.EmbeddedDashboardResponse{}
	requestedPanels := sets.NewString(in.Request.PanelTitles...)
	now := time.Now().Unix()
	for _, p := range board.Panels {
		if p.Type == "row" {
			continue
		}
		if requestedPanels.Len() > 0 && !requestedPanels.Has(p.Title) {
			continue
		}

		// url template: "http://{{.URL}}/d-solo/{{.BoardUID}}/{{.DashboardName}}?orgId={{.OrgID}}&from={{.From}}&to={{.To}}&theme={{.Theme}}&panelId="
		url := fmt.Sprintf("%v/d-solo/%v/%v?orgId=%v&from=%v&to=%v&panelId=%v", grafanaHost, board.UID, *dashboard.Status.Dashboard.Slug, *dashboard.Status.Dashboard.OrgID, now, now, p.ID)
		panelURL := uiapi.PanelURL{
			Title:       p.Title,
			EmbeddedURL: url,
		}
		in.Response.URLs = append(in.Response.URLs, panelURL)
	}

	return in, nil
}
