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
	"k8s.io/klog/v2"
	kmapi "kmodules.xyz/client-go/api/v1"
	clustermeta "kmodules.xyz/client-go/cluster"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type ClientOrgReconciler struct {
	kc         client.Client
	apiReader  client.Reader
	scheme     *runtime.Scheme
	pc         *prometheus.Client
	clusterUID string
	hubUID     string
	d          detector.PrometheusDetector
}

func NewReconciler(kc client.Client, apiReader client.Reader, pc *prometheus.Client, clusterUID, hubUID string, d detector.PrometheusDetector) *ClientOrgReconciler {
	return &ClientOrgReconciler{
		kc:         kc,
		apiReader:  apiReader,
		scheme:     kc.Scheme(),
		pc:         pc,
		clusterUID: clusterUID,
		hubUID:     hubUID,
		d:          d,
	}
}

const (
	srcRefKey  = "meta.appcode.com/source"
	srcHashKey = "meta.appcode.com/hash"
	// Guards the "terminating"-label teardown so it runs once, not on every reconcile.
	cleanedUpKey = "meta.appcode.com/cleaned-up"

	crClientOrgMonitoring = "appscode:client-org:monitoring"
	abClientOrgGrafana    = "grafana"
	abClientOrgPerses     = "perses"
)

func (r *ClientOrgReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.V(4).Infof("reconciling client-org Namespace %s", req.Name)

	var ns core.Namespace
	if err := r.kc.Get(ctx, req.NamespacedName, &ns); err != nil {
		klog.V(4).Infof("Namespace %s not found, ignoring: %v", req.Name, err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if ns.Labels[kmapi.ClientOrgKey] == "" {
		klog.V(4).Infof("Namespace %s has no %s label, skipping", ns.Name, kmapi.ClientOrgKey)
		return ctrl.Result{}, nil
	}
	clientOrgId := ns.Annotations[kmapi.AceOrgIDKey]
	if clientOrgId == "" {
		klog.V(4).Infof("Namespace %s has no %s annotation, skipping", ns.Name, kmapi.AceOrgIDKey)
		return ctrl.Result{}, nil
	}
	klog.V(4).Infof("Namespace %s resolved to client-org %s (label=%q)", ns.Name, clientOrgId, ns.Labels[kmapi.ClientOrgKey])

	if ready, err := r.d.Ready(); err != nil || !ready {
		klog.V(4).Infof("Prometheus detector not ready for Namespace %s (ready=%t, err=%v), requeueing", ns.Name, ready, err)
		return ctrl.Result{}, err
	}
	if r.d.RancherManaged() && r.d.Federated() {
		return ctrl.Result{}, fmt.Errorf("client organization mode is not supported when federated Prometheus is used in a Rancher managed cluster")
	}

	if ns.DeletionTimestamp != nil {
		klog.Infof("client-org namespace %s (org %s) is terminating, unregistering backends", ns.Name, clientOrgId)
		return r.handleDeletion(ctx, ns, clientOrgId)
	}

	// Teardown can also happen by flipping the label to "terminating" instead of deleting the
	// namespace; run the same cleanup once, guarded by cleanedUpKey.
	if ns.Labels[kmapi.ClientOrgKey] == "terminating" {
		if ns.Annotations[cleanedUpKey] == "true" {
			return ctrl.Result{}, nil
		}
		klog.Infof("client-org namespace %s (org %s) label set to terminating, unregistering backends", ns.Name, clientOrgId)
		if _, err := r.handleDeletion(ctx, ns, clientOrgId); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.markCleanedUp(ctx, &ns)
	}

	// The cache lags the API server and the ServiceAccount watch re-enqueues every client-org
	// namespace on each trickster token refresh, so a teardown-time reconcile can observe a
	// stale, live-looking namespace and recreate dashboards cleanup just removed. Re-read from
	// the API server and bail if it is no longer an active client org.
	var fresh core.Namespace
	if err := r.apiReader.Get(ctx, req.NamespacedName, &fresh); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !isActiveClientOrg(&fresh) {
		klog.V(4).Infof("Namespace %s is no longer an active client-org on fresh read, skipping", ns.Name)
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
	klog.V(4).Infof("using monitoring namespace %s for client-org namespace %s", monNamespace.Name, ns.Name)

	if err := r.ensureMonitoringRoleBinding(ctx, monNamespace, clientOrgId); err != nil {
		return ctrl.Result{}, err
	}

	var promList monitoringv1.PrometheusList
	if err := r.kc.List(ctx, &promList); err != nil {
		return ctrl.Result{}, err
	}
	prom, ok := detector.DefaultPrometheus(r.kc.RESTMapper(), r.d, promList.Items)
	if !ok {
		return ctrl.Result{}, errors.New("failed to detect default Prometheus")
	}
	promKey := client.ObjectKeyFromObject(prom)
	klog.V(4).Infof("detected default Prometheus %s for client-org namespace %s", promKey.String(), ns.Name)

	var svcProm core.Service
	if err := r.kc.Get(context.TODO(), r.d.ServiceKey(promKey), &svcProm); err != nil {
		return ctrl.Result{}, err
	}

	pcfg, err := r.buildPrometheusConfig(ctx, promKey, svcProm)
	if err != nil {
		return ctrl.Result{}, err
	}

	if r.pc != nil {
		cm, err := clustermeta.ClusterMetadata(r.kc)
		if err != nil {
			return ctrl.Result{}, err
		}
		klog.V(4).Infof("registering backends for client-org %s in namespace %s (state=%s)", clientOrgId, monNamespace.Name, cm.State())
		if err := r.registerBackends(ctx, &monNamespace, pcfg, clientOrgId, cm.State()); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		klog.V(4).Infof("backend client is nil, skipping backend registration for client-org %s", clientOrgId)
	}

	klog.V(4).Infof("copying dashboards for client-org namespace %s into %s", ns.Name, monNamespace.Name)
	if err := r.copyDashboards(ctx, ns, monNamespace); err != nil {
		return ctrl.Result{}, err
	}
	klog.V(4).Infof("finished reconciling client-org namespace %s", ns.Name)
	return ctrl.Result{}, nil
}

func (r *ClientOrgReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&core.Namespace{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			v := obj.GetLabels()[kmapi.ClientOrgKey]
			return (v == "true" || v == "terminating") &&
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
