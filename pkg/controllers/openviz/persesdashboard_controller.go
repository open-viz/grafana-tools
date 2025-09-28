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

package openviz

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/pkg/perses"

	v1 "github.com/perses/perses/pkg/model/api/v1"
	"github.com/perses/perses/pkg/model/api/v1/dashboard"
	"github.com/perses/perses/pkg/model/api/v1/variable"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	kmapi "kmodules.xyz/client-go/api/v1"
	kmc "kmodules.xyz/client-go/client"
	condutil "kmodules.xyz/client-go/conditions"
	meta_util "kmodules.xyz/client-go/meta"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	PersesDashboardFinalizer = "persesdashboard.openviz.dev/finalizer"
)

// PersesDashboardReconciler reconciles a GrafanaDashboard object
type PersesDashboardReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Recorder             record.EventRecorder
	RequeueAfterDuration time.Duration
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *PersesDashboardReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	key := req.NamespacedName

	obj := &openvizapi.PersesDashboard{}
	if err := r.Client.Get(ctx, key, obj); err != nil {
		klog.Infof("Perses Dashboard %q doesn't exist anymore", req.NamespacedName.String())
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	obj = obj.DeepCopy()

	if obj.ObjectMeta.DeletionTimestamp != nil {
		// Change the Phase to Terminating if not
		if obj.Status.Phase != openvizapi.GrafanaPhaseTerminating {
			_, err := kmc.PatchStatus(ctx, r.Client, obj, func(obj client.Object) client.Object {
				in := obj.(*openvizapi.PersesDashboard)
				in.Status.Phase = openvizapi.GrafanaPhaseTerminating
				in.Status.Reason = "Resource has been going to be deleted"
				return in
			})
			if err != nil {
				return ctrl.Result{}, err
			}
		}
		// Delete the external dashboard
		if err := r.deleteExternalDashboard(ctx, obj); err != nil {
			return ctrl.Result{}, err
		}

		// Remove finalizer as the external Dashboard is successfully deleted
		_, err := kmc.CreateOrPatch(ctx, r.Client, obj, func(obj client.Object, createOp bool) client.Object {
			controllerutil.RemoveFinalizer(obj, PersesDashboardFinalizer)
			return obj
		})
		return ctrl.Result{}, err
	}

	// Add finalizer if not set
	if !containsString(obj.GetFinalizers(), PersesDashboardFinalizer) {
		_, err := kmc.CreateOrPatch(ctx, r.Client, obj, func(obj client.Object, createOp bool) client.Object {
			controllerutil.AddFinalizer(obj, PersesDashboardFinalizer)
			return obj
		})
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// Set the Phase to Processing if the dashboard is going to be processed for the first time.
	// If the dashboard phase is already failed then setting Phase is skipped.
	if obj.Status.Phase != openvizapi.GrafanaPhaseProcessing && obj.Status.Phase != openvizapi.GrafanaPhaseFailed {
		_, err := kmc.PatchStatus(ctx, r.Client, obj, func(obj client.Object) client.Object {
			in := obj.(*openvizapi.PersesDashboard)
			in.Status.Phase = openvizapi.GrafanaPhaseProcessing
			return in
		})
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	klog.Infof("Reconciling for: %s", key.String())
	return r.setDashboard(ctx, obj)
}

func (r *PersesDashboardReconciler) handleSetDashboardError(ctx context.Context, obj *openvizapi.PersesDashboard, err error, updateGeneration bool) (ctrl.Result, error) {
	reason := err.Error()
	r.recordFailureEvent(obj, reason)
	_, patchErr := kmc.PatchStatus(ctx, r.Client, obj, func(obj client.Object) client.Object {
		in := obj.(*openvizapi.PersesDashboard)
		in.Status.Phase = openvizapi.GrafanaPhaseFailed
		in.Status.Reason = reason
		if updateGeneration {
			in.Status.ObservedGeneration = in.Generation
		}
		in.Status.Conditions = condutil.SetCondition(in.Status.Conditions, kmapi.Condition{
			Type:    condutil.ConditionFailed,
			Status:  metav1.ConditionTrue,
			Reason:  reason,
			Message: reason,
		})
		return in
	})
	return ctrl.Result{RequeueAfter: r.RequeueAfterDuration}, patchErr
}

// SetupWithManager sets up the controller with the Manager.
func (r *PersesDashboardReconciler) SetupWithManager(mgr ctrl.Manager) error {
	appHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []reconcile.Request {
		var dashboardList openvizapi.PersesDashboardList
		err := r.Client.List(ctx, &dashboardList)
		if err != nil {
			return nil
		}

		var req []reconcile.Request
		for _, db := range dashboardList.Items {
			ab, err := perses.GetPerses(ctx, r.Client, db.Spec.PersesRef.WithNamespace(db.Namespace))
			if err != nil {
				return nil
			}

			if ab.Name == a.GetName() &&
				ab.Namespace == a.GetNamespace() &&
				(db.Status.Phase == openvizapi.GrafanaPhaseFailed || IsPersesDashboardStateChanged(&db, a.(*appcatalog.AppBinding))) {
				req = append(req, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&db)})
			}
		}
		return req
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&openvizapi.PersesDashboard{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			db := obj.(*openvizapi.PersesDashboard)
			if !meta_util.MustAlreadyReconciled(obj) {
				return true
			}
			if ab, err := perses.GetPerses(context.TODO(), r.Client, db.Spec.PersesRef.WithNamespace(db.Namespace)); err == nil {
				return IsPersesDashboardStateChanged(db, ab)
			}
			return false
		}))).
		Watches(&appcatalog.AppBinding{}, appHandler).
		Complete(r)
}

func IsPersesDashboardStateChanged(db *openvizapi.PersesDashboard, ab *appcatalog.AppBinding) bool {
	if db.Status.Dashboard == nil || db.Status.Dashboard.State == nil {
		return false
	}
	curState, err := GrafanaState(ab)
	if err != nil {
		return false
	}
	return curState != *db.Status.Dashboard.State
}

func (r *PersesDashboardReconciler) deleteExternalDashboard(ctx context.Context, obj *openvizapi.PersesDashboard) error {
	if obj.Status.Dashboard != nil && obj.Status.Dashboard.Name != "" {
		pc, err := perses.NewPersesClient(ctx, r.Client, obj.Spec.PersesRef.WithNamespace(obj.Namespace))
		if err != nil {
			return err
		}

		err = pc.DeleteDashboardByName(obj.Status.Dashboard)
		if err != nil {
			return err
		}

	}
	return nil
}

func (r *PersesDashboardReconciler) setDashboard(ctx context.Context, obj *openvizapi.PersesDashboard) (ctrl.Result, error) {
	ab, err := perses.GetPerses(ctx, r.Client, obj.Spec.PersesRef.WithNamespace(obj.Namespace))
	if err != nil {
		return r.handleSetDashboardError(ctx, obj, err, false)
	}

	// Grafana state == Perses state
	state, err := GrafanaState(ab)
	if err != nil {
		return r.handleSetDashboardError(ctx, obj, err, false)
	}

	dsConfig := &openvizapi.PersesConfiguration{}
	if ab.Spec.Parameters != nil {
		if err := json.Unmarshal(ab.Spec.Parameters.Raw, dsConfig); err != nil {
			return r.handleSetDashboardError(ctx, obj, fmt.Errorf("failed to unmarshal app binding parameters, reason: %v", err), false)
		}
	}

	pc, err := perses.NewPersesClient(ctx, r.Client, obj.Spec.PersesRef.WithNamespace(obj.Namespace))
	if err != nil {
		return r.handleSetDashboardError(ctx, obj, err, false)
	}

	var pDB v1.Dashboard
	if err = json.Unmarshal(obj.Spec.Model.Raw, &pDB); err != nil {
		return r.handleSetDashboardError(ctx, obj, err, false)
	}

	pDB.Metadata.Project = dsConfig.ProjectName
	pDB.Metadata.FolderName = dsConfig.FolderName

	if err := updateDatasourceVariable(&pDB, dsConfig.Datasource); err != nil {
		return r.handleSetDashboardError(ctx, obj, err, false)
	}

	err = pc.SetDashboard(ctx, pDB, dsConfig.ProjectName, dsConfig.FolderName)
	if err != nil {
		return r.handleSetDashboardError(ctx, obj, err, false)
	}

	_, err = kmc.PatchStatus(ctx, r.Client, obj, func(obj client.Object) client.Object {
		in := obj.(*openvizapi.PersesDashboard)
		reason := "Dashboard is successfully created"
		in.Status.Dashboard = &openvizapi.PersesDashboardReference{
			Name:        pDB.Metadata.Name,
			ProjectName: dsConfig.ProjectName,
			FolderName:  dsConfig.FolderName,
			State:       &state,
		}
		in.Status.Phase = openvizapi.GrafanaPhaseCurrent
		in.Status.ObservedGeneration = in.Generation
		in.Status.Conditions = condutil.SetCondition(in.Status.Conditions, kmapi.Condition{
			Type:    condutil.ConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  reason,
			Message: reason,
		})
		in.Status.Reason = reason
		return in
	})
	return ctrl.Result{}, err
}

func (r *PersesDashboardReconciler) recordFailureEvent(obj *openvizapi.PersesDashboard, reason string) {
	r.Recorder.Eventf(
		obj,
		core.EventTypeWarning,
		reason,
		`Failed to complete operation for PeresDashboard: "%v", Reason: "%v"`,
		obj.Name,
		reason)
}

func updateDatasourceVariable(pDB *v1.Dashboard, ds string) error {
	for i := range pDB.Spec.Variables {
		v := &pDB.Spec.Variables[i]

		if v.Kind != "ListVariable" {
			continue
		}

		spec, ok := v.Spec.(*dashboard.ListVariableSpec)
		if !ok {
			return fmt.Errorf("variable %s is not ListVariableSpec", v.Spec.GetName())
		}

		if spec.GetName() == "datasource" {
			spec.DefaultValue = &variable.DefaultValue{
				SingleValue: ds,
			}
			v.Spec = spec
			continue
		}

		pluginSpec, ok := spec.Plugin.Spec.(map[string]any)
		if !ok {
			continue
		}

		if _, exists := pluginSpec["datasource"]; !exists {
			pluginSpec["datasource"] = map[string]any{
				"kind": "PrometheusDatasource",
				"name": ds,
			}
		}

		v.Spec = spec
	}

	for k, panel := range pDB.Spec.Panels {
		for qi := range panel.Spec.Queries {
			query := &panel.Spec.Queries[qi]
			if query.Kind != "TimeSeriesQuery" {
				continue
			}
			if query.Spec.Plugin.Kind != "PrometheusTimeSeriesQuery" {
				continue
			}

			spec, ok := query.Spec.Plugin.Spec.(map[string]any)
			if !ok {
				continue
			}

			if _, exists := spec["datasource"]; !exists {
				spec["datasource"] = map[string]any{
					"kind": "PrometheusDatasource",
					"name": ds,
				}
			}

			query.Spec.Plugin.Spec = spec
		}

		pDB.Spec.Panels[k] = panel
	}

	return nil
}
