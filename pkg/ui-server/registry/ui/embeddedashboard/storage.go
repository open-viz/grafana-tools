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
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
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
		gr: openvizapi.Resource(openvizapi.ResourceGrafanaDashboards),
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

	var grafanadashboard openvizapi.GrafanaDashboard
	if in.Request.Ref.Name != nil {
		err := r.kc.Get(ctx, client.ObjectKey{Namespace: ns, Name: *in.Request.Ref.Name}, &grafanadashboard)
		if err != nil {
			return nil, err
		}
	} else if in.Request.Ref.Selector != nil {
		var grafanadashboardList openvizapi.GrafanaDashboardList
		if err := r.kc.List(ctx, &grafanadashboardList, client.InNamespace(ns), client.MatchingFields{
			openvizapi.GrafanaNameKey:           in.Request.Ref.Selector.GrafanaName,
			openvizapi.GrafanaDashboardTitleKey: in.Request.Ref.Selector.DashboardTitle,
		}); err != nil {
			return nil, err
		}
		if len(grafanadashboardList.Items) == 0 {
			return nil, apierrors.NewNotFound(openvizapi.Resource(openvizapi.ResourceGrafanaDashboards), fmt.Sprintf("%+v", in.Request.Ref.Selector))
		} else if len(grafanadashboardList.Items) > 1 {
			return nil, apierrors.NewBadRequest(fmt.Sprintf("%+v selects multiple grafanadashboards", in.Request.Ref.Selector))
		}
		grafanadashboard = grafanadashboardList.Items[0]
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
		Name:      grafanadashboard.Name,
	}
	decision, why, err := r.a.Authorize(ctx, attrs)
	if err != nil {
		return nil, apierrors.NewInternalError(err)
	}
	if decision != authorizer.DecisionAllow {
		return nil, apierrors.NewForbidden(r.gr, grafanadashboard.Name, errors.New(why))
	}

	if grafanadashboard.Spec.Grafana == nil {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("GrafanaDashboard %s/%s is missing a Grafana ref", grafanadashboard.Namespace, grafanadashboard.Name))
	}
	if grafanadashboard.Status.Dashboard == nil {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("Status.Dashboard field is missing in GrafanaDashboard %s/%s", grafanadashboard.Namespace, grafanadashboard.Name))
	}
	var ab appcatalogapi.AppBinding
	abKey := client.ObjectKey{Namespace: grafanadashboard.Namespace, Name: grafanadashboard.Spec.Grafana.Name}
	err = r.kc.Get(ctx, abKey, &ab)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch AppBinding %s, reason: %v", abKey, err)
	}
	grafanaHost, err := ab.URL()
	if err != nil {
		return nil, err
	}

	if grafanadashboard.Spec.Model == nil {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("GrafanaDashboard %s/%s is missing a model", grafanadashboard.Namespace, grafanadashboard.Name))
	}
	board := &sdk.Board{}
	err = json.Unmarshal(grafanadashboard.Spec.Model.Raw, board)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal model for GrafanaDashboard %s/%s, reason: %v", grafanadashboard.Namespace, grafanadashboard.Name, err)
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

		baseURL, err := url.Parse(grafanaHost)
		if err != nil {
			return nil, apierrors.NewInternalError(err)
		}

		baseURL.Path = path.Join(baseURL.Path, "d-solo", *grafanadashboard.Status.Dashboard.UID, *grafanadashboard.Status.Dashboard.Slug)
		q := url.Values{}
		q.Add("ordID", strconv.Itoa(int(*grafanadashboard.Status.Dashboard.OrgID)))
		q.Add("var-namespace", "namespacehere")
		q.Add("var-dbtype", "dbnamehere") // example: For Postgres dbtype will be postgres(same as grafana dashboard and case insensitive)
		q.Add("from", strconv.Itoa(int(now)))
		q.Add("to", strconv.Itoa(int(now)))
		q.Add("panelId", strconv.Itoa(int(p.ID)))
		baseURL.RawQuery = q.Encode()

		//url-example:  http://kube-prometheus-stack-grafana.monitoring.svc:80/d-solo/7oanhmhnk/kubedb-postgres-summary?
		//              from=1638527684&ordID=1&panelId=36&to=1638527684&var-postgres=dbnamehere&var-namespace=demo

		panelURL := uiapi.PanelURL{
			Title:       p.Title,
			EmbeddedURL: baseURL.String(),
		}
		in.Response.URLs = append(in.Response.URLs, panelURL)
	}

	return in, nil
}
