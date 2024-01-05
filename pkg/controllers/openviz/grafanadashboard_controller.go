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
	"net/http"
	"time"

	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	sdk "go.openviz.dev/grafana-sdk"
	"go.openviz.dev/grafana-tools/pkg/grafana"

	"gomodules.xyz/pointer"
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
	GrafanaDashboardFinalizer = "grafanadashboard.openviz.dev/finalizer"
)

// GrafanaDashboardReconciler reconciles a GrafanaDashboard object
type GrafanaDashboardReconciler struct {
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
func (r *GrafanaDashboardReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	key := req.NamespacedName

	obj := &openvizapi.GrafanaDashboard{}
	if err := r.Client.Get(ctx, key, obj); err != nil {
		klog.Infof("Grafana Dashboard %q doesn't exist anymore", req.NamespacedName.String())
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	obj = obj.DeepCopy()

	if obj.ObjectMeta.DeletionTimestamp != nil {
		// Change the Phase to Terminating if not
		if obj.Status.Phase != openvizapi.GrafanaPhaseTerminating {
			_, err := kmc.PatchStatus(ctx, r.Client, obj, func(obj client.Object) client.Object {
				in := obj.(*openvizapi.GrafanaDashboard)
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
			controllerutil.RemoveFinalizer(obj, GrafanaDashboardFinalizer)
			return obj
		})
		return ctrl.Result{}, err
	}

	// Add finalizer if not set
	if !containsString(obj.GetFinalizers(), GrafanaDashboardFinalizer) {
		_, err := kmc.CreateOrPatch(ctx, r.Client, obj, func(obj client.Object, createOp bool) client.Object {
			controllerutil.AddFinalizer(obj, GrafanaDashboardFinalizer)
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
			in := obj.(*openvizapi.GrafanaDashboard)
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

func (r *GrafanaDashboardReconciler) handleSetDashboardError(ctx context.Context, obj *openvizapi.GrafanaDashboard, err error, updateGeneration bool) (ctrl.Result, error) {
	reason := err.Error()
	r.recordFailureEvent(obj, reason)
	_, patchErr := kmc.PatchStatus(ctx, r.Client, obj, func(obj client.Object) client.Object {
		in := obj.(*openvizapi.GrafanaDashboard)
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
func (r *GrafanaDashboardReconciler) SetupWithManager(mgr ctrl.Manager) error {
	appHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []reconcile.Request {
		var dashboardList openvizapi.GrafanaDashboardList
		err := r.Client.List(ctx, &dashboardList, client.InNamespace(a.GetNamespace()))
		if err != nil {
			return nil
		}

		var req []reconcile.Request
		for _, db := range dashboardList.Items {
			ab, err := openvizapi.GetGrafana(ctx, r.Client, db.Spec.GrafanaRef.WithNamespace(db.Namespace))
			if err != nil {
				return nil
			}
			if ab.Name == a.GetName() &&
				ab.Namespace == a.GetNamespace() &&
				db.Status.Phase == openvizapi.GrafanaPhaseFailed {
				req = append(req, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&db)})
			}
		}
		return req
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvizapi.GrafanaDashboard{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return !meta_util.MustAlreadyReconciled(obj)
		}))).
		Watches(&appcatalog.AppBinding{}, appHandler).
		Complete(r)
}

func (r *GrafanaDashboardReconciler) deleteExternalDashboard(ctx context.Context, obj *openvizapi.GrafanaDashboard) error {
	if obj.Status.Dashboard != nil && obj.Status.Dashboard.UID != nil {
		gc, err := grafana.NewGrafanaClient(ctx, r.Client, obj.Spec.GrafanaRef.WithNamespace(obj.Namespace))
		if err != nil {
			return err
		}
		resp, err := gc.DeleteDashboardByUID(ctx, *obj.Status.Dashboard.UID)
		if err != nil {
			// Finalizer is removed in case remote Grafana Dashboard doesn't exist anymore
			if resp != nil && resp.StatusCode == http.StatusNotFound {
				return nil
			}
			return err
		}

	}
	return nil
}

func (r *GrafanaDashboardReconciler) setDashboard(ctx context.Context, obj *openvizapi.GrafanaDashboard) (ctrl.Result, error) {
	if obj.Status.Dashboard != nil {
		model, err := addDashboardID(obj.Spec.Model.Raw, *obj.Status.Dashboard.ID, *obj.Status.Dashboard.UID)
		if err != nil {
			return r.handleSetDashboardError(ctx, obj, err, false)
		}
		obj.Spec.Model = &runtime.RawExtension{Raw: model}
	}
	ab, err := openvizapi.GetGrafana(ctx, r.Client, obj.Spec.GrafanaRef.WithNamespace(obj.Namespace))
	if err != nil {
		return r.handleSetDashboardError(ctx, obj, err, false)
	}

	dsConfig := &openvizapi.GrafanaConfiguration{}
	if ab.Spec.Parameters != nil {
		if err := json.Unmarshal(ab.Spec.Parameters.Raw, dsConfig); err != nil {
			return r.handleSetDashboardError(ctx, obj, fmt.Errorf("failed to unmarshal app binding parameters, reason: %v", err), false)
		}
	}

	if obj.Spec.Templatize != nil && obj.Spec.Templatize.Datasource {
		if ab.Spec.Parameters == nil {
			return r.handleSetDashboardError(ctx, obj, fmt.Errorf("failed to templatize dashboard, reason: datasource parameter is not provided in app binding %s/%s", ab.Namespace, ab.Name), false)
		}
		model, err := replaceDatasource(obj.Spec.Model.Raw, dsConfig.Datasource)
		if err != nil {
			return r.handleSetDashboardError(ctx, obj, err, false)
		}
		obj.Spec.Model = &runtime.RawExtension{Raw: model}
	}

	// collect grafana url and auth info from app binding
	gc, err := grafana.NewGrafanaClient(ctx, r.Client, obj.Spec.GrafanaRef.WithNamespace(obj.Namespace))
	if err != nil {
		return r.handleSetDashboardError(ctx, obj, err, false)
	}
	gDB := &sdk.GrafanaDashboard{
		Dashboard: obj.Spec.Model,
		FolderId:  int(pointer.Int64(dsConfig.FolderID)),
		Overwrite: true,
	}
	resp, err := gc.SetDashboard(ctx, gDB)
	if err != nil {
		// Update Observed generation if GrafanaDashboard json configuration is invalid
		// Ref: https://grafana.com/docs/grafana/latest/http_api/dashboard/#create--update-dashboard
		if resp != nil && resp.StatusCode == http.StatusBadRequest {
			return r.handleSetDashboardError(ctx, obj, err, true)
		}
		return r.handleSetDashboardError(ctx, obj, err, false)
	}
	orgId, err := gc.GetCurrentOrg(ctx)
	if err != nil {
		return r.handleSetDashboardError(ctx, obj, err, false)
	}

	_, err = kmc.PatchStatus(ctx, r.Client, obj, func(obj client.Object) client.Object {
		in := obj.(*openvizapi.GrafanaDashboard)
		reason := "Dashboard is successfully created"
		in.Status.Dashboard = &openvizapi.GrafanaDashboardReference{
			ID:      pointer.Int64P(int64(pointer.Int(resp.ID))),
			UID:     resp.UID,
			Slug:    resp.Slug,
			URL:     resp.URL,
			OrgID:   pointer.Int64P(int64(pointer.Int(orgId.ID))),
			Version: pointer.Int64P(int64(pointer.Int(resp.Version))),
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

// Helper function to add dashboard id of the created dashboard in dashboard model json while updating
func addDashboardID(model []byte, id int64, uid string) ([]byte, error) {
	val := make(map[string]interface{})
	err := json.Unmarshal(model, &val)
	if err != nil {
		return nil, err
	}
	val["id"] = id
	val["uid"] = uid
	return json.Marshal(val)
}

// Helper function to replace the datasource name in dashboard panels
func getUpdatedPanels(panels []interface{}, ds string) []interface{} {
	updatedPanels := make([]interface{}, 0)
	for _, p := range panels {
		panel, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		panel["datasource"] = ds

		collapsed, found := panel["collapsed"].(bool)
		if found && collapsed {
			nestedPanels, ok := panel["panels"].([]interface{})
			if ok {
				updatedNestedPanels := getUpdatedPanels(nestedPanels, ds)
				panel["panels"] = updatedNestedPanels
			}
		}
		updatedPanels = append(updatedPanels, panel)
	}
	return updatedPanels
}

// Helper function to replace datasource of the given dashboard model
func replaceDatasource(model []byte, ds string) ([]byte, error) {
	val := make(map[string]interface{})
	err := json.Unmarshal(model, &val)
	if err != nil {
		return nil, err
	}
	panels, ok := val["panels"].([]interface{})
	if !ok {
		return model, nil
	}
	val["panels"] = getUpdatedPanels(panels, ds)

	templateList, ok := val["templating"].(map[string]interface{})
	if !ok {
		return json.Marshal(val)
	}
	templateVars, ok := templateList["list"].([]interface{})
	if !ok {
		return json.Marshal(val)
	}

	var newVars []interface{}
	for _, v := range templateVars {
		vr, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		ty, ok := vr["type"].(string)
		if !ok {
			continue
		}
		vr["datasource"] = ds
		if ty != "datasource" {
			newVars = append(newVars, vr)
		}
	}
	templateList["list"] = newVars

	return json.Marshal(val)
}

func (r *GrafanaDashboardReconciler) recordFailureEvent(obj *openvizapi.GrafanaDashboard, reason string) {
	r.Recorder.Eventf(
		obj,
		core.EventTypeWarning,
		reason,
		`Failed to complete operation for GrafanaDashboard: "%v", Reason: "%v"`,
		obj.Name,
		reason)
}
