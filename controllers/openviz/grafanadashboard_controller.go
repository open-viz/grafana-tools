/*
Copyright 2021.

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
	"errors"
	"fmt"

	sdk "go.openviz.dev/grafana-sdk"
	openvizapi "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	"gomodules.xyz/pointer"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	kmc "kmodules.xyz/client-go/client"
	meta_util "kmodules.xyz/client-go/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	GrafanaDashboardFinalizer = "grafanadashboard.openviz.dev/finalizer"
)

// GrafanaDashboardReconciler reconciles a GrafanaDashboard object
type GrafanaDashboardReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=openviz.appscode.com,resources=grafanadashboards,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=openviz.appscode.com,resources=grafanadashboards/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=openviz.appscode.com,resources=grafanadashboards/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *GrafanaDashboardReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	key := req.NamespacedName

	db := &openvizapi.GrafanaDashboard{}
	if err := r.Client.Get(ctx, key, db); err != nil {
		klog.Infof("Grafana Dashboard %q doesn't exist anymore", req.NamespacedName.String())
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Add or remove finalizer based on deletion timestamp
	if db.ObjectMeta.DeletionTimestamp.IsZero() {
		if !containsString(db.GetFinalizers(), GrafanaDashboardFinalizer) {
			_, _, err := kmc.CreateOrPatch(r.Client, db, func(obj client.Object, createOp bool) client.Object {
				controllerutil.AddFinalizer(obj, GrafanaDashboardFinalizer)
				return obj
			})
			if err != nil {
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		}
	} else {
		if containsString(db.GetFinalizers(), GrafanaDashboardFinalizer) {
			_, _, err := kmc.PatchStatus(r.Client, db, func(obj client.Object, createOp bool) client.Object {
				in := obj.(*openvizapi.GrafanaDashboard)
				in.Status.Phase = openvizapi.GrafanaPhaseTerminating
				in.Status.ObservedGeneration = in.Generation
				return in
			})
			if err != nil {
				return ctrl.Result{}, err
			}

			if err := r.deleteExternalDashboard(ctx, db); err != nil {
				return ctrl.Result{}, err
			}

			_, _, err = kmc.CreateOrPatch(r.Client, db, func(obj client.Object, createOp bool) client.Object {
				controllerutil.RemoveFinalizer(obj, GrafanaDashboardFinalizer)
				return obj
			})
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	if !meta_util.MustAlreadyReconciled(db) {
		_, _, err := kmc.PatchStatus(r.Client, db, func(obj client.Object, createOp bool) client.Object {
			in := obj.(*openvizapi.GrafanaDashboard)
			in.Status.Phase = openvizapi.GrafanaPhaseProcessing
			in.Status.ObservedGeneration = in.Generation
			return in
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		klog.Infof("Reconciling for: %s", key.String())

		if err := r.setDashboard(ctx, db); err != nil {
			_, _, stsErr := kmc.PatchStatus(r.Client, db, func(obj client.Object, createOp bool) client.Object {
				in := obj.(*openvizapi.GrafanaDashboard)
				in.Status.Phase = openvizapi.GrafanaPhaseFailed
				in.Status.Reason = err.Error()
				return in
			})
			if stsErr != nil {
				return ctrl.Result{}, stsErr
			}
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GrafanaDashboardReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvizapi.GrafanaDashboard{}).
		Complete(r)
}

func (r *GrafanaDashboardReconciler) deleteExternalDashboard(ctx context.Context, db *openvizapi.GrafanaDashboard) error {
	if db.Status.Dashboard != nil && db.Status.Dashboard.UID != nil {
		gc, err := getGrafanaClient(ctx, r.Client, db.Spec.GrafanaRef)
		if err != nil {
			return err
		}
		if _, err = gc.DeleteDashboardByUID(ctx, *db.Status.Dashboard.UID); err != nil {
			return err
		}
	}
	return nil
}

func (r *GrafanaDashboardReconciler) setDashboard(ctx context.Context, db *openvizapi.GrafanaDashboard) error {
	if db.Status.Dashboard != nil {
		model, err := addDashboardID(db.Spec.Model.Raw, *db.Status.Dashboard.ID, *db.Status.Dashboard.UID)
		if err != nil {
			return err
		}
		db.Spec.Model = &runtime.RawExtension{Raw: model}
	}
	ab, err := getAppBinding(ctx, r.Client, db.Spec.GrafanaRef)
	if err != nil {
		return err
	}

	if db.Spec.Templatize != nil && db.Spec.Templatize.Datasource {
		dsConfig := &openvizapi.GrafanaConfiguration{}
		if ab.Spec.Parameters != nil {
			err := json.Unmarshal(ab.Spec.Parameters.Raw, &dsConfig)
			if err != nil {
				return fmt.Errorf("failed to unmarshal app binding parameters, reason: %v", err)
			}
		} else {
			return errors.New("failed to templatize dashboard, reason: dashboard parameter is not provided in the app binding")
		}
		model, err := replaceDatasource(db.Spec.Model.Raw, dsConfig.Datasource)
		if err != nil {
			return err
		}
		db.Spec.Model = &runtime.RawExtension{Raw: model}
	}

	// collect grafana url and auth info from app binding
	gc, err := getGrafanaClient(ctx, r.Client, db.Spec.GrafanaRef)
	if err != nil {
		return err
	}
	gDB := &sdk.GrafanaDashboard{
		Dashboard: db.Spec.Model,
		FolderId:  int(pointer.Int64(db.Spec.FolderID)),
		Overwrite: true,
	}
	resp, err := gc.SetDashboard(ctx, gDB)
	if err != nil {
		return err
	}
	orgId, err := gc.GetCurrentOrg(ctx)
	if err != nil {
		return err
	}

	_, _, err = kmc.PatchStatus(r.Client, db, func(obj client.Object, createOp bool) client.Object {
		in := obj.(*openvizapi.GrafanaDashboard)
		in.Status.Dashboard = &openvizapi.GrafanaDashboardReference{
			ID:      pointer.Int64P(int64(pointer.Int(resp.ID))),
			UID:     resp.UID,
			Slug:    resp.Slug,
			URL:     resp.URL,
			OrgID:   pointer.Int64P(int64(pointer.Int(orgId.ID))),
			Version: pointer.Int64P(int64(pointer.Int(resp.Version))),
		}
		in.Status.Phase = openvizapi.GrafanaPhaseCurrent
		return in
	})
	if err != nil {
		return err
	}
	return nil
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
	var updatedPanels []interface{}
	for _, p := range panels {
		panel, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		panel["datasource"] = ds
		updatedPanels = append(updatedPanels, panel)
	}
	val["panels"] = updatedPanels

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
