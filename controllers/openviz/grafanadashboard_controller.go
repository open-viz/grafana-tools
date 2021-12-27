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
	"errors"

	sdk "go.openviz.dev/grafana-sdk"
	openvizv1alpha1 "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	"gomodules.xyz/pointer"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	kmapi "kmodules.xyz/client-go/api/v1"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
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
	klog.Infof("Reconciling for: %s", key.String())

	db := &openvizv1alpha1.GrafanaDashboard{}
	if err := r.Client.Get(ctx, key, db); err != nil {
		klog.Infof("Grafana Dashboard %q doesn't exist anymore", req.NamespacedName.String())
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Add or remove finalizer based on deletion timestamp
	if db.ObjectMeta.DeletionTimestamp.IsZero() {
		if !containsString(db.GetFinalizers(), GrafanaDashboardFinalizer) {
			controllerutil.AddFinalizer(db, GrafanaDashboardFinalizer)
			if err := r.Update(ctx, db); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		if containsString(db.GetFinalizers(), GrafanaDashboardFinalizer) {
			if err := r.deleteExternalResources(db); err != nil {
				return ctrl.Result{}, err
			}

			controllerutil.RemoveFinalizer(db, GrafanaDashboardFinalizer)
			if err := r.Update(ctx, db); err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	if err := r.setDashboard(ctx, db); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GrafanaDashboardReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvizv1alpha1.GrafanaDashboard{}).
		Complete(r)
}

func (r *GrafanaDashboardReconciler) deleteExternalResources(db *openvizv1alpha1.GrafanaDashboard) error {
	return nil
}

func (r *GrafanaDashboardReconciler) setDashboard(ctx context.Context, db *openvizv1alpha1.GrafanaDashboard) error {
	// collect grafana url and auth info from app binding
	if db.Spec.GrafanaRef == nil {
		return errors.New("grafana app binding info is missing")
	}
	gc, err := r.getGrafanaClient(ctx, db.Spec.GrafanaRef)
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

	db.Status.Dashboard = &openvizv1alpha1.GrafanaDashboardReference{
		ID:      pointer.Int64P(int64(pointer.Int(resp.ID))),
		UID:     resp.UID,
		Slug:    resp.Slug,
		URL:     resp.URL,
		OrgID:   pointer.Int64P(int64(pointer.Int(orgId.ID))),
		Version: pointer.Int64P(int64(pointer.Int(resp.Version))),
	}

	if err := r.Client.Status().Update(ctx, db); err != nil {
		return err
	}
	return nil
}

func (r *GrafanaDashboardReconciler) getGrafanaClient(ctx context.Context, ref *kmapi.ObjectReference) (*sdk.Client, error) {
	ab := &appcatalog.AppBinding{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, ab); err != nil {
		return nil, err
	}
	auth := &core.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ab.Spec.Secret.Name}, auth); err != nil {
		return nil, err
	}
	gURL, err := ab.URL()
	if err != nil {
		return nil, err
	}
	apiKey, ok := auth.Data["apiKey"]
	if !ok {
		return nil, errors.New("apiKey is not provided")
	}
	gc, err := sdk.NewClient(gURL, string(apiKey))
	if err != nil {
		return nil, err
	}
	return gc, nil
}

// Helper functions to check a string is present from a slice of strings.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
