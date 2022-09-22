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

package dashboardgroup

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	uiapi "go.openviz.dev/apimachinery/apis/ui/v1alpha1"

	"github.com/grafana-tools/sdk"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
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
	_ rest.Storage                  = &Storage{}
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
	return uiapi.SchemeGroupVersion.WithKind(uiapi.ResourceKindDashboardGroup)
}

func (r *Storage) NamespaceScoped() bool {
	return false
}

func (r *Storage) New() runtime.Object {
	return &uiapi.DashboardGroup{}
}

func (r *Storage) Destroy() {}

func (r *Storage) Create(ctx context.Context, obj runtime.Object, _ rest.ValidateObjectFunc, _ *metav1.CreateOptions) (runtime.Object, error) {
	in := obj.(*uiapi.DashboardGroup)
	if in.Request == nil {
		return nil, apierrors.NewBadRequest("missing apirequest")
	}

	in.Response = &uiapi.DashboardGroupResponse{
		Dashboards: make([]uiapi.DashboardResponse, 0, len(in.Request.Dashboards)),
	}
	for _, req := range in.Request.Dashboards {
		resp, err := r.getDashboardLink(ctx, &req, in.Request.RefreshInterval, in.Request.TimeRange, in.Request.EmbeddedLink)
		if err != nil {
			return nil, err
		} else {
			in.Response.Dashboards = append(in.Response.Dashboards, *resp)
		}
	}

	return in, nil
}

func (r *Storage) getDashboardLink(
	ctx context.Context,
	req *uiapi.DashboardRequest,
	refreshInterval string,
	timeRange *uiapi.TimeRange,
	embed bool,
) (*uiapi.DashboardResponse, error) {
	var d openvizapi.GrafanaDashboard
	if req.ObjectReference != nil {
		ns := req.Namespace
		if ns == "" {
			return nil, fmt.Errorf("missing namespace for Dashboard")
		}
		err := r.kc.Get(ctx, client.ObjectKey{Namespace: ns, Name: req.Name}, &d)
		if err != nil {
			return nil, err
		}
	} else if req.Title != "" {
		var dashboardList openvizapi.GrafanaDashboardList
		// any namespace, using default grafana and with the given title
		if err := r.kc.List(ctx, &dashboardList, client.MatchingFields{
			mona.DefaultGrafanaKey:              "true",
			openvizapi.GrafanaDashboardTitleKey: req.Title,
		}); err != nil {
			return nil, err
		}
		if len(dashboardList.Items) == 0 {
			return nil, apierrors.NewNotFound(openvizapi.Resource(openvizapi.ResourceKindGrafanaDashboard), fmt.Sprintf("No dashboard with title %s uses the default Grafana", req.Title))
		} else if len(dashboardList.Items) > 1 {
			names := make([]string, len(dashboardList.Items))
			for idx, item := range dashboardList.Items {
				names[idx] = fmt.Sprintf("%s/%s", item.Namespace, item.Name)
			}
			return nil, apierrors.NewBadRequest(fmt.Sprintf("multiple dashboards %s with title %s uses the default Grafana", strings.Join(names, ","), req.Title))
		}
		d = dashboardList.Items[0]
	}

	user, ok := apirequest.UserFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("missing user info")
	}

	{
		attrs := authorizer.AttributesRecord{
			User:      user,
			Verb:      "get",
			Namespace: d.Namespace,
			APIGroup:  r.gr.Group,
			Resource:  r.gr.Resource,
			Name:      d.Name,
		}
		decision, why, err := r.a.Authorize(ctx, attrs)
		if err != nil {
			return nil, apierrors.NewInternalError(err)
		}
		if decision != authorizer.DecisionAllow {
			return nil, apierrors.NewForbidden(r.gr, d.Name, errors.New(why))
		}
	}
	if d.Status.Dashboard == nil {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("Status.Dashboard field is missing in GrafanaDashboard %s/%s", d.Namespace, d.Name))
	}

	g, err := openvizapi.GetGrafana(ctx, r.kc, d.Spec.GrafanaRef.WithNamespace(d.Namespace))
	if err != nil {
		return nil, err
	}

	{
		attrs := authorizer.AttributesRecord{
			User:      user,
			Verb:      "get",
			Namespace: g.Namespace,
			APIGroup:  appcatalog.GroupName,
			Resource:  appcatalogapi.ResourceApps,
			Name:      g.Name,
		}
		decision, why, err := r.a.Authorize(ctx, attrs)
		if err != nil {
			return nil, apierrors.NewInternalError(err)
		}
		if decision != authorizer.DecisionAllow {
			return nil, apierrors.NewForbidden(r.gr, g.Name, errors.New(why))
		}
	}

	grafanaHost, err := g.URL()
	if err != nil {
		return nil, err
	}

	if d.Spec.Model == nil {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("GrafanaDashboard %s/%s is missing a model", d.Namespace, d.Name))
	}
	board := &sdk.Board{}
	err = json.Unmarshal(d.Spec.Model.Raw, board)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal model for GrafanaDashboard %s/%s, reason: %v", d.Namespace, d.Name, err)
	}

	resp := &uiapi.DashboardResponse{
		DashboardRef: req.DashboardRef,
	}
	if embed {
		resp.Panels = make([]uiapi.PanelLinkResponse, 0, len(board.Panels))

		panelMap := map[string]int{}
		for _, p := range req.Panels {
			panelMap[p.Title] = p.Width
		}

		for _, p := range board.Panels {
			if p.Type == "row" {
				for _, p2 := range p.RowPanel.Panels {
					if panel, err := toEmbeddedPanel(&p2, grafanaHost, d, refreshInterval, timeRange, req, panelMap); err != nil {
						return nil, err
					} else if panel != nil {
						resp.Panels = append(resp.Panels, *panel)
					}
				}
			} else {
				if panel, err := toEmbeddedPanel(p, grafanaHost, d, refreshInterval, timeRange, req, panelMap); err != nil {
					return nil, err
				} else if panel != nil {
					resp.Panels = append(resp.Panels, *panel)
				}
			}
		}
	} else {
		// http://localhost:3000/d/85a562078cdf77779eaa1add43ccec1e/kubernetes-compute-resources-namespace-pods?orgId=1&refresh=10s&from=1647757465219&to=1647761065220

		baseURL, err := url.Parse(grafanaHost)
		if err != nil {
			return nil, apierrors.NewInternalError(err)
		}

		// if embedded
		baseURL.Path = path.Join(baseURL.Path, "d", *d.Status.Dashboard.UID, *d.Status.Dashboard.Slug)
		q := url.Values{}
		q.Add("orgId", strconv.Itoa(int(*d.Status.Dashboard.OrgID)))
		if refreshInterval == "" {
			q.Add("refresh", "30s")
		} else {
			q.Add("refresh", refreshInterval)
		}
		if timeRange == nil {
			q.Add("from", "now-3h")
			q.Add("to", "now")
		} else {
			q.Add("from", timeRange.From)
			q.Add("to", timeRange.To)
		}
		baseURL.RawQuery = addVars(q, req.Vars)

		resp.URL = baseURL.String()
	}

	return resp, nil
}

func toEmbeddedPanel(p *sdk.Panel, grafanaHost string, d openvizapi.GrafanaDashboard, refreshInterval string, timeRange *uiapi.TimeRange, req *uiapi.DashboardRequest, panelMap map[string]int) (*uiapi.PanelLinkResponse, error) {
	includePanel := func(title string) bool {
		if len(panelMap) == 0 {
			return true
		}
		_, ok := panelMap[title]
		return ok
	}

	if !includePanel(p.Title) {
		return nil, nil
	}

	// Embedded URL
	// <iframe src="http://localhost:3000/d-solo/200ac8fdbfbb74b39aff88118e4d1c2c/kubernetes-compute-resources-node-pods?orgId=1&refresh=10s&from=1647592158580&to=1647595758580&panelId=1" width="450" height="200" frameborder="0"></iframe>

	baseURL, err := url.Parse(grafanaHost)
	if err != nil {
		return nil, apierrors.NewInternalError(err)
	}

	// if embedded
	baseURL.Path = path.Join(baseURL.Path, "d-solo", *d.Status.Dashboard.UID, *d.Status.Dashboard.Slug)
	q := url.Values{}
	q.Add("orgId", strconv.Itoa(int(*d.Status.Dashboard.OrgID)))
	if refreshInterval == "" {
		q.Add("refresh", "30s")
	} else {
		q.Add("refresh", refreshInterval)
	}
	if timeRange == nil {
		q.Add("from", "now-3h")
		q.Add("to", "now")
	} else {
		q.Add("from", timeRange.From)
		q.Add("to", timeRange.To)
	}
	q.Add("panelId", strconv.Itoa(int(p.ID)))
	baseURL.RawQuery = addVars(q, req.Vars)

	return &uiapi.PanelLinkResponse{
		Title: p.Title,
		URL:   baseURL.String(),
		Width: panelMap[p.Title],
	}, nil
}

func addVars(q url.Values, vars []uiapi.DashboardVar) string {
	srcVars := 0
	targetVars := 0
	for _, v := range vars {
		if v.Type == uiapi.DashboardVarTypeTarget {
			targetVars++
			continue
		}
		q.Add(v.VarName(), v.Value)
		srcVars++
	}
	rawQuery := q.Encode()

	if targetVars > 0 {
		var buf strings.Builder
		for _, v := range vars {
			if v.Type != uiapi.DashboardVarTypeTarget {
				continue
			}
			if buf.Len() > 0 {
				buf.WriteByte('&')
			}
			buf.WriteString(url.QueryEscape(v.VarName()))
			buf.WriteByte('=')
			buf.WriteString(v.Value)
		}
		if srcVars > 0 {
			rawQuery += "&"
		} else {
			rawQuery += "?"
		}
		rawQuery += buf.String()
	}
	return rawQuery
}
