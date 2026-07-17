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
	"context"
	"errors"
	"fmt"

	"go.openviz.dev/grafana-tools/pkg/controllers/prometheus"
	"go.openviz.dev/grafana-tools/pkg/detector"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kmapi "kmodules.xyz/client-go/api/v1"
	clustermeta "kmodules.xyz/client-go/cluster"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ClientOrgReconciler reconciles a GatewayConfig object
type ClientOrgReconciler struct {
	kc         client.Client
	apiReader  client.Reader
	scheme     *runtime.Scheme
	bc         *prometheus.Client
	clusterUID string
	hubUID     string
	d          detector.PrometheusDetector
}

func NewReconciler(kc client.Client, apiReader client.Reader, bc *prometheus.Client, clusterUID, hubUID string, d detector.PrometheusDetector) *ClientOrgReconciler {
	return &ClientOrgReconciler{
		kc:         kc,
		apiReader:  apiReader,
		scheme:     kc.Scheme(),
		bc:         bc,
		clusterUID: clusterUID,
		hubUID:     hubUID,
		d:          d,
	}
}

const (
	srcRefKey  = "meta.appcode.com/source"
	srcHashKey = "meta.appcode.com/hash"

	// cr created via monitoring-operator chart
	crClientOrgMonitoring = "appscode:client-org:monitoring"
	abClientOrgGrafana    = "grafana"
	abClientOrgPerses     = "perses"
)

func (r *ClientOrgReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var ns core.Namespace
	if err := r.kc.Get(ctx, req.NamespacedName, &ns); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if ns.Labels[kmapi.ClientOrgKey] == "" || ns.Labels[kmapi.ClientOrgKey] == "terminating" {
		return ctrl.Result{}, nil
	}
	clientOrgId := ns.Annotations[kmapi.AceOrgIDKey]
	if clientOrgId == "" {
		return ctrl.Result{}, nil
	}

	if ready, err := r.d.Ready(); err != nil || !ready {
		return ctrl.Result{}, err
	}
	if r.d.RancherManaged() && r.d.Federated() {
		return ctrl.Result{}, fmt.Errorf("client organization mode is not supported when federated Prometheus is used in a Rancher managed cluster")
	}

	if ns.DeletionTimestamp != nil {
		return r.handleDeletion(ctx, ns, clientOrgId)
	}

	// The cached client lags the API server, and the ServiceAccount watch re-enqueues every
	// client-org namespace on each trickster token refresh. A reconcile fired during teardown
	// can therefore still observe a stale, live-looking namespace and recreate the copied
	// dashboards that cleanup just removed. Re-read from the API server before (re)creating
	// anything and bail out if the namespace is no longer an active client org.
	var fresh core.Namespace
	if err := r.apiReader.Get(ctx, req.NamespacedName, &fresh); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !isActiveClientOrg(&fresh) {
		return ctrl.Result{}, nil
	}

	if err := r.ensureFinalizer(ctx, &ns); err != nil {
		return ctrl.Result{}, err
	}

	monNamespace, result, err := r.ensureMonitoringNamespace(ctx, ns)
	if err != nil {
		return ctrl.Result{}, err
	}
	if result.RequeueAfter > 0 {
		return result, nil
	}

	if err := r.ensureMonitoringRoleBinding(ctx, monNamespace, clientOrgId); err != nil {
		return ctrl.Result{}, err
	}

	// confirm trickster rb registered
	var promList monitoringv1.PrometheusList
	if err := r.kc.List(ctx, &promList); err != nil {
		return ctrl.Result{}, err
	}
	prom, ok := detector.DefaultPrometheus(r.kc.RESTMapper(), r.d, promList.Items)
	if !ok {
		return ctrl.Result{}, errors.New("failed to detect default Prometheus")
	}
	promKey := client.ObjectKeyFromObject(prom)

	var svcProm core.Service
	if err := r.kc.Get(context.TODO(), r.d.ServiceKey(promKey), &svcProm); err != nil {
		return ctrl.Result{}, err
	}

	pcfg, err := r.buildPrometheusConfig(ctx, promKey, svcProm)
	if err != nil {
		return ctrl.Result{}, err
	}

	if r.bc != nil {
		cm, err := clustermeta.ClusterMetadata(r.kc)
		if err != nil {
			return ctrl.Result{}, err
		}
		if err := r.registerBackends(ctx, &monNamespace, pcfg, clientOrgId, cm.State()); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, r.copyDashboards(ctx, ns, monNamespace)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClientOrgReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&core.Namespace{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetLabels()[kmapi.ClientOrgKey] == "true" &&
				obj.GetLabels()[kmapi.ClientOrgMonitoringKey] != "false"
		}))).
		Watches(&core.ServiceAccount{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []reconcile.Request {
			var list core.NamespaceList
			if err := r.kc.List(ctx, &list, client.MatchingLabels{
				kmapi.ClientOrgKey: "true",
			}); err != nil {
				return nil
			}
			reqs := make([]reconcile.Request, 0, len(list.Items))
			for _, ns := range list.Items {
				if ns.DeletionTimestamp != nil {
					continue
				}
				reqs = append(reqs, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: ns.Name,
					},
				})
			}
			return reqs
		}), builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetName() == prometheus.ServiceAccountTrickster &&
				obj.GetLabels()[prometheus.RegisteredKey] != ""
		}))).
		Named("namespace").
		Complete(r)
}
