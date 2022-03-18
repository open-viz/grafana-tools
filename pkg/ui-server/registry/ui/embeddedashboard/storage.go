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

package embeddegrafanadashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	openvizapi "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	uiapi "go.openviz.dev/grafana-tools/apis/ui/v1alpha1"

	"github.com/grafana-tools/sdk"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"kmodules.xyz/custom-resources/apis/appcatalog"
	appcatalogapi "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Storage struct {
	kc client.Client
	a  authorizer.Authorizer
	gr schema.GroupResource
}

var (
	_ rest.GroupVersionKindProvider = &Storage{}
	_ rest.Scoper                   = &Storage{}
	_ rest.Creater                  = &Storage{}
)

func NewStorage(kc client.Client, a authorizer.Authorizer) *Storage {
	return &Storage{
		kc: kc,
		a:  a,
		gr: openvizapi.Resource(openvizapi.ResourceGrafanaDashboards),
	}
}

func (r *Storage) GroupVersionKind(_ schema.GroupVersion) schema.GroupVersionKind {
	return uiapi.SchemeGroupVersion.WithKind(uiapi.ResourceKindEmbeddedDashboard)
}

func (r *Storage) NamespaceScoped() bool {
	return false
}

func (r *Storage) New() runtime.Object {
	return &uiapi.EmbeddedDashboard{}
}

func (r *Storage) Create(ctx context.Context, obj runtime.Object, _ rest.ValidateObjectFunc, _ *metav1.CreateOptions) (runtime.Object, error) {
	in := obj.(*uiapi.EmbeddedDashboard)
	if in.Request == nil {
		return nil, apierrors.NewBadRequest("missing apirequest")
	}

	var dashboard openvizapi.GrafanaDashboard
	if in.Request.Dashboard.ObjectReference != nil {
		ns := in.Request.Dashboard.Namespace
		if ns == "" {
			return nil, fmt.Errorf("missing namespace for Dashboard")
		}
		err := r.kc.Get(ctx, client.ObjectKey{Namespace: ns, Name: in.Request.Dashboard.Name}, &dashboard)
		if err != nil {
			return nil, err
		}
	} else if in.Request.Dashboard.Title != "" {
		var dashboardList openvizapi.GrafanaDashboardList
		// any namespace, using default grafana and with the given title
		if err := r.kc.List(ctx, &dashboardList, client.MatchingFields{
			mona.DefaultGrafanaKey:              "true",
			openvizapi.GrafanaDashboardTitleKey: in.Request.Dashboard.Title,
		}); err != nil {
			return nil, err
		}
		if len(dashboardList.Items) == 0 {
			return nil, apierrors.NewNotFound(openvizapi.Resource(openvizapi.ResourceKindGrafanaDashboard), fmt.Sprintf("No dashboard with title %s uses the default Grafana", in.Request.Dashboard.Title))
		} else if len(dashboardList.Items) > 1 {
			names := make([]string, len(dashboardList.Items))
			for idx, item := range dashboardList.Items {
				names[idx] = fmt.Sprintf("%s/%s", item.Namespace, item.Name)
			}
			return nil, apierrors.NewBadRequest(fmt.Sprintf("multiple dashboards %s with title %s uses the default Grafana", strings.Join(names, ","), in.Request.Dashboard.Title))
		}
		dashboard = dashboardList.Items[0]
	}

	user, ok := apirequest.UserFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("missing user info")
	}

	{
		attrs := authorizer.AttributesRecord{
			User:      user,
			Verb:      "get",
			Namespace: dashboard.Namespace,
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
	}
	if dashboard.Status.Dashboard == nil {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("Status.Dashboard field is missing in GrafanaDashboard %s/%s", dashboard.Namespace, dashboard.Name))
	}

	grafana, err := openvizapi.GetGrafana(ctx, r.kc, dashboard.Spec.GrafanaRef.WithNamespace(dashboard.Namespace))
	if err != nil {
		return nil, err
	}

	{
		attrs := authorizer.AttributesRecord{
			User:      user,
			Verb:      "get",
			Namespace: grafana.Namespace,
			APIGroup:  appcatalog.GroupName,
			Resource:  appcatalogapi.ResourceApps,
			Name:      grafana.Name,
		}
		decision, why, err := r.a.Authorize(ctx, attrs)
		if err != nil {
			return nil, apierrors.NewInternalError(err)
		}
		if decision != authorizer.DecisionAllow {
			return nil, apierrors.NewForbidden(r.gr, grafana.Name, errors.New(why))
		}
	}

	grafanaHost, err := grafana.URL()
	if err != nil {
		return nil, err
	}

	if dashboard.Spec.Model == nil {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("GrafanaDashboard %s/%s is missing a model", dashboard.Namespace, dashboard.Name))
	}
	board := &sdk.Board{}
	err = json.Unmarshal(dashboard.Spec.Model.Raw, board)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal model for GrafanaDashboard %s/%s, reason: %v", dashboard.Namespace, dashboard.Name, err)
	}

	in.Response = &uiapi.EmbeddedDashboardResponse{}
	requestedPanels := sets.NewString(in.Request.Panels...)
	// from := strconv.FormatInt(time.Now().Add(-time.Hour*1).UnixMilli(), 10)
	// to := strconv.FormatInt(time.Now().UnixMilli(), 10)

	for _, p := range board.Panels {
		if p.Type == "row" {
			continue
		}
		if requestedPanels.Len() > 0 && !requestedPanels.Has(p.Title) {
			continue
		}

		// <iframe src="http://localhost:3000/d-solo/200ac8fdbfbb74b39aff88118e4d1c2c/kubernetes-compute-resources-node-pods?orgId=1&refresh=10s&from=1647592158580&to=1647595758580&panelId=1" width="450" height="200" frameborder="0"></iframe>

		baseURL, err := url.Parse(grafanaHost)
		if err != nil {
			return nil, apierrors.NewInternalError(err)
		}

		baseURL.Path = path.Join(baseURL.Path, "d-solo", *dashboard.Status.Dashboard.UID, *dashboard.Status.Dashboard.Slug)
		q := url.Values{}
		q.Add("orgId", strconv.Itoa(int(*dashboard.Status.Dashboard.OrgID)))
		q.Add("refresh", "30s")
		q.Add("from", "now-1h")
		q.Add("to", "now")
		q.Add("panelId", strconv.Itoa(int(p.ID)))
		q.Add("var-namespace", in.Request.Target.Namespace)
		// q.Add("var-name", in.Request.Target.Name)
		q.Add("var-"+strings.ToLower(in.Request.Target.Kind), in.Request.Target.Name)
		baseURL.RawQuery = q.Encode()

		panelURL := uiapi.PanelURL{
			Title:       p.Title,
			EmbeddedURL: baseURL.String(),
		}
		in.Response.URLs = append(in.Response.URLs, panelURL)
	}

	return in, nil
}
