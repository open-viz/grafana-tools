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
	"fmt"

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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	GrafanaDatasourceFinalizer = "grafanadatasource.openviz.dev/finalizer"
)

// GrafanaDatasourceReconciler reconciles a GrafanaDatasource object
type GrafanaDatasourceReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *GrafanaDatasourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	key := req.NamespacedName

	ds := &openvizapi.GrafanaDatasource{}
	if err := r.Get(ctx, key, ds); err != nil {
		klog.Infof("Grafana Datasource %q doesn't exist anymore", req.String())
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Add or remove finalizer based on deletion timestamp
	if ds.DeletionTimestamp.IsZero() {
		if !containsString(ds.GetFinalizers(), GrafanaDatasourceFinalizer) {
			_, err := kmc.CreateOrPatch(ctx, r.Client, ds, func(obj client.Object, createOp bool) client.Object {
				controllerutil.AddFinalizer(obj, GrafanaDatasourceFinalizer)
				return obj
			})
			if err != nil {
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, nil
		}
	} else {
		if containsString(ds.GetFinalizers(), GrafanaDatasourceFinalizer) {
			_, err := kmc.PatchStatus(ctx, r.Client, ds, func(obj client.Object) client.Object {
				in := obj.(*openvizapi.GrafanaDatasource)
				in.Status.Phase = openvizapi.GrafanaPhaseTerminating
				return in
			})
			if err != nil {
				return ctrl.Result{}, err
			}

			if err := r.deleteExternalDatasource(ctx, ds); err != nil {
				return ctrl.Result{}, err
			}

			_, err = kmc.CreateOrPatch(ctx, r.Client, ds, func(obj client.Object, createOp bool) client.Object {
				controllerutil.RemoveFinalizer(obj, GrafanaDatasourceFinalizer)
				return obj
			})
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	if !meta_util.MustAlreadyReconciled(ds) {
		_, err := kmc.PatchStatus(ctx, r.Client, ds, func(obj client.Object) client.Object {
			in := obj.(*openvizapi.GrafanaDatasource)
			in.Status.Phase = openvizapi.GrafanaPhaseProcessing
			in.Status.Conditions = []kmapi.Condition{}
			in.Status.ObservedGeneration = in.Generation
			return in
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		klog.Infof("Reconciling for: %s", key.String())

		if err := r.createOrUpdateDatasource(ctx, ds); err != nil {
			r.handleFailureEvent(ctx, ds, err.Error())
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GrafanaDatasourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvizapi.GrafanaDatasource{}).
		Complete(r)
}

func (r *GrafanaDatasourceReconciler) createOrUpdateDatasource(ctx context.Context, ds *openvizapi.GrafanaDatasource) error {
	dataSrc := sdk.Datasource{
		OrgID:     uint(ds.Spec.OrgID),
		Name:      ds.Spec.Name,
		Type:      string(ds.Spec.Type),
		Access:    string(ds.Spec.Access),
		URL:       ds.Spec.URL,
		IsDefault: ds.Spec.IsDefault,
	}

	gc, err := grafana.NewGrafanaClient(ctx, r.Client, ds.Spec.GrafanaRef.WithNamespace(ds.Namespace))
	if err != nil {
		return err
	}
	if ds.Status.GrafanaDatasourceID != nil {
		dataSrc.ID = uint(pointer.Int64(ds.Status.GrafanaDatasourceID))
		err := r.updateDatasource(ctx, gc, ds, dataSrc)
		if err != nil {
			return fmt.Errorf("failed to update Datasource, reason: %v", err)
		}
	} else {
		err := r.createDatasource(ctx, gc, ds, dataSrc)
		if err != nil {
			return fmt.Errorf("failed to create Datasource, reason: %v", err)
		}
	}

	return nil
}

func (r *GrafanaDatasourceReconciler) deleteExternalDatasource(ctx context.Context, ds *openvizapi.GrafanaDatasource) error {
	if ds.Status.GrafanaDatasourceID != nil {
		gc, err := grafana.NewGrafanaClient(ctx, r.Client, ds.Spec.GrafanaRef.WithNamespace(ds.Namespace))
		if err != nil {
			return err
		}
		dsID := int(pointer.Int64(ds.Status.GrafanaDatasourceID))
		if _, err = gc.DeleteDatasource(ctx, dsID); err != nil {
			return err
		}
	}
	return nil
}

func (r *GrafanaDatasourceReconciler) createDatasource(ctx context.Context, gc *sdk.Client, ds *openvizapi.GrafanaDatasource, dataSrc sdk.Datasource) error {
	statusMsg, err := gc.CreateDatasource(ctx, &dataSrc)
	if err != nil {
		return fmt.Errorf("failed to create datasource, reason: %v", err.Error())
	}
	klog.Infof("GrafanaDatasource is created with message: %s\n", pointer.String(statusMsg.Message))

	_, err = kmc.PatchStatus(ctx, r.Client, ds, func(obj client.Object) client.Object {
		in := obj.(*openvizapi.GrafanaDatasource)
		in.Status.Phase = openvizapi.GrafanaPhaseCurrent
		in.Status.Reason = "Successfully created Grafana Datasource"
		in.Status.ObservedGeneration = in.Generation
		in.Status.GrafanaDatasourceID = pointer.Int64P(int64(pointer.Int(statusMsg.ID)))

		return in
	})
	if err != nil {
		return fmt.Errorf("failed to update GrafanaDatasource phase to %q", openvizapi.GrafanaPhaseCurrent)
	}
	return nil
}

func (r *GrafanaDatasourceReconciler) updateDatasource(ctx context.Context, gc *sdk.Client, ds *openvizapi.GrafanaDatasource, dataSrc sdk.Datasource) error {
	statusMsg, err := gc.UpdateDatasource(ctx, dataSrc)
	if err != nil {
		return fmt.Errorf("failed to update remote datasource, reason: %v", err)
	}
	klog.Infof("GrafanaDatasource is updated with message: %s\n", pointer.String(statusMsg.Message))
	_, err = kmc.PatchStatus(ctx, r.Client, ds, func(obj client.Object) client.Object {
		in := obj.(*openvizapi.GrafanaDatasource)
		in.Status.Phase = openvizapi.GrafanaPhaseCurrent
		in.Status.Reason = "Successfully updated GrafanaDatasource"
		in.Status.ObservedGeneration = ds.Generation
		return in
	})
	if err != nil {
		return fmt.Errorf("failed to update GrafanaDatasource phase to %q", openvizapi.GrafanaPhaseCurrent)
	}
	return nil
}

func (r *GrafanaDatasourceReconciler) handleFailureEvent(ctx context.Context, ds *openvizapi.GrafanaDatasource, reason string) {
	r.Recorder.Eventf(
		ds,
		core.EventTypeWarning,
		reason,
		`Failed to complete operation for GrafanaDatasource: "%v", Reason: "%v"`,
		ds.Name,
		reason)
	_, err := kmc.PatchStatus(ctx, r.Client, ds, func(obj client.Object) client.Object {
		in := obj.(*openvizapi.GrafanaDatasource)
		in.Status.Phase = openvizapi.GrafanaPhaseFailed
		in.Status.Reason = reason
		in.Status.Conditions = condutil.SetCondition(in.Status.Conditions, kmapi.Condition{
			Type:    condutil.ConditionFailed,
			Status:  metav1.ConditionTrue,
			Reason:  reason,
			Message: reason,
		})
		return in
	})
	if err != nil {
		r.Recorder.Eventf(ds, core.EventTypeWarning, "failed to update status", err.Error())
	}
}
