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

	openvizv1alpha1 "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
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
// TODO(user): Modify the Reconcile function to compare the state specified by
// the GrafanaDashboard object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
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

// Helper functions to check a string is present from a slice of strings.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
