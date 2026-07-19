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

package namespace

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"

	"github.com/grafana-tools/sdk"
	v1 "github.com/perses/perses/pkg/model/api/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/json"
	kutil "kmodules.xyz/client-go"
	kmapi "kmodules.xyz/client-go/api/v1"
	cu "kmodules.xyz/client-go/client"
	clustermeta "kmodules.xyz/client-go/cluster"
	"kmodules.xyz/client-go/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func (r *ClientOrgReconciler) copyDashboards(ctx context.Context, ns, monNamespace core.Namespace) error {
	var errList []error

	errListGrafana, err := r.copyGrafanaDashboards(ctx, ns, monNamespace)
	if err != nil {
		return err
	}
	errList = append(errList, errListGrafana...)

	errListPerses, err := r.copyPersesDashboards(ctx, monNamespace)
	if err != nil {
		return err
	}
	errList = append(errList, errListPerses...)

	return utilerrors.NewAggregate(errList)
}

// copyGrafanaDashboards copies every source GrafanaDashboard into the client monitoring
// namespace, pinning the "namespace" templating variable to the client-org namespace. The
// returned slice holds per-dashboard soft errors; a returned error aborts the reconcile.
func (r *ClientOrgReconciler) copyGrafanaDashboards(ctx context.Context, ns, monNamespace core.Namespace) ([]error, error) {
	log := log.FromContext(ctx)

	var dashboardList openvizapi.GrafanaDashboardList
	if err := r.kc.List(ctx, &dashboardList); err != nil {
		return nil, err
	}

	var errList []error
	for _, dashboard := range dashboardList.Items {
		if _, isCopy := dashboard.Annotations[srcRefKey]; isCopy || strings.HasSuffix(dashboard.Namespace, "-monitoring") {
			continue
		}
		if dashboard.Spec.Model == nil {
			return nil, apierrors.NewBadRequest(fmt.Sprintf("GrafanaDashboard %s/%s is missing a model", dashboard.Namespace, dashboard.Name))
		}
		var board sdk.Board
		err := json.Unmarshal(dashboard.Spec.Model.Raw, &board)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal model for GrafanaDashboard %s/%s, reason: %v", dashboard.Namespace, dashboard.Name, err)
		}
		for idx, item := range board.Templating.List {
			if item.Name != "namespace" {
				continue
			}

			item.Type = "constant"
			item.Query = ns.Name
			board.Templating.List[idx] = item
		}
		board.Title = clustermeta.ClientDashboardTitle(board.Title)
		boardBytes, err := json.Marshal(board)
		if err != nil {
			return nil, err
		}

		copiedDashboard := &openvizapi.GrafanaDashboard{}
		err = r.kc.Get(context.TODO(), types.NamespacedName{
			Namespace: monNamespace.Name,
			Name:      dashboard.Name,
		}, copiedDashboard)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, err
			}
			copiedDashboard = dashboard.DeepCopy()
			copiedDashboard.Namespace = monNamespace.Name
		} else {
			copiedDashboard.Spec = *dashboard.Spec.DeepCopy()
		}

		opresult, err := cu.CreateOrPatch(ctx, r.kc, copiedDashboard, func(obj client.Object, createOp bool) client.Object {
			if createOp {
				copiedDashboard.ResourceVersion = ""
			}

			if copiedDashboard.Annotations == nil {
				copiedDashboard.Annotations = map[string]string{}
			}
			copiedDashboard.Annotations[srcRefKey] = client.ObjectKeyFromObject(&dashboard).String()
			copiedDashboard.Annotations[srcHashKey] = meta.ObjectHash(&dashboard)

			copiedDashboard.Spec.GrafanaRef = &kmapi.ObjectReference{
				Namespace: monNamespace.Name,
				Name:      abClientOrgGrafana,
			}
			copiedDashboard.Spec.Model = &runtime.RawExtension{
				Raw: boardBytes,
			}

			return copiedDashboard
		})
		if err != nil {
			errList = append(errList, err)
		} else if opresult != kutil.VerbUnchanged {
			log.Info(fmt.Sprintf("%s GrafanaDashboard %s/%s", opresult, copiedDashboard.Namespace, copiedDashboard.Name))
		}
	}

	return errList, nil
}

// copyPersesDashboards copies every source PersesDashboard into the client monitoring
// namespace. The returned slice holds per-dashboard soft errors; a returned error aborts
// the reconcile.
func (r *ClientOrgReconciler) copyPersesDashboards(ctx context.Context, monNamespace core.Namespace) ([]error, error) {
	log := log.FromContext(ctx)

	var persesDashboardList openvizapi.PersesDashboardList
	if err := r.kc.List(ctx, &persesDashboardList); err != nil {
		return nil, err
	}

	var errList []error
	for _, dashboard := range persesDashboardList.Items {
		if dashboard.Spec.Model == nil {
			return nil, apierrors.NewBadRequest(fmt.Sprintf("PersesDashboard %s/%s is missing a model", dashboard.Namespace, dashboard.Name))
		}
		var board v1.Dashboard
		err := json.Unmarshal(dashboard.Spec.Model.Raw, &board)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal model for GrafanaDashboard %s/%s, reason: %v", dashboard.Namespace, dashboard.Name, err)
		}

		board.Spec.Display.Name = clustermeta.ClientDashboardTitle(board.Spec.Display.Name)
		boardBytes, err := json.Marshal(board)
		if err != nil {
			return nil, err
		}

		if _, found := dashboard.Annotations[srcRefKey]; found && bytes.Equal(dashboard.Spec.Model.Raw, boardBytes) {
			continue
		}

		copiedDashboard := &openvizapi.PersesDashboard{}
		err = r.kc.Get(context.TODO(), types.NamespacedName{
			Namespace: monNamespace.Name,
			Name:      dashboard.Name,
		}, copiedDashboard)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, err
			}
			copiedDashboard = dashboard.DeepCopy()
			copiedDashboard.Namespace = monNamespace.Name
		} else {
			copiedDashboard.Spec = *dashboard.Spec.DeepCopy()
		}

		opresult, err := cu.CreateOrPatch(ctx, r.kc, copiedDashboard, func(obj client.Object, createOp bool) client.Object {
			if createOp {
				copiedDashboard.ResourceVersion = ""
			}

			if copiedDashboard.Annotations == nil {
				copiedDashboard.Annotations = map[string]string{}
			}
			copiedDashboard.Annotations[srcRefKey] = client.ObjectKeyFromObject(&dashboard).String()
			copiedDashboard.Annotations[srcHashKey] = meta.ObjectHash(&dashboard)

			copiedDashboard.Spec.PersesRef = &kmapi.ObjectReference{
				Namespace: monNamespace.Name,
				Name:      abClientOrgPerses,
			}
			copiedDashboard.Spec.Model = &runtime.RawExtension{
				Raw: boardBytes,
			}

			return copiedDashboard
		})
		if err != nil {
			errList = append(errList, err)
		} else if opresult != kutil.VerbUnchanged {
			log.Info(fmt.Sprintf("%s PersesDashboard %s/%s", opresult, copiedDashboard.Namespace, copiedDashboard.Name))
		}
	}

	return errList, nil
}
