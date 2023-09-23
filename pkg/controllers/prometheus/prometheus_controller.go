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

package prometheus

import (
	"context"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"kmodules.xyz/resource-metadata/client/clientset/versioned"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// PrometheusReconciler reconciles a Prometheus object
type PrometheusReconciler struct {
	cfg        *rest.Config
	rmc        versioned.Interface
	kc         client.Client
	scheme     *runtime.Scheme
	bc         *Client
	clusterUID string
}

func NewReconciler(cfg *rest.Config, rmc versioned.Interface, kc client.Client, bc *Client, clusterUID string) *PrometheusReconciler {
	return &PrometheusReconciler{
		cfg:        cfg,
		rmc:        rmc,
		kc:         kc,
		scheme:     kc.Scheme(),
		bc:         bc,
		clusterUID: clusterUID,
	}
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Prometheus object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.0/pkg/reconcile
func (r *PrometheusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var prom monitoringv1.Prometheus
	if err := r.kc.Get(ctx, req.NamespacedName, &prom); err != nil {
		log.Error(err, "unable to fetch Prometheus")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := r.SetupClusterForPrometheus(req.NamespacedName); err != nil {
		log.Error(err, "unable to setup Prometheus")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PrometheusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&monitoringv1.Prometheus{}).
		Complete(r)
}
