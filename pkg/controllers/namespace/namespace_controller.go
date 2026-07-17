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
	"time"

	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/pkg/controllers/clientorg"
	"go.openviz.dev/grafana-tools/pkg/controllers/prometheus"
	"go.openviz.dev/grafana-tools/pkg/detector"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	kmapi "kmodules.xyz/client-go/api/v1"
	cu "kmodules.xyz/client-go/client"
	clustermeta "kmodules.xyz/client-go/cluster"
	core_util "kmodules.xyz/client-go/core/v1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
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
		if r.bc != nil {
			err := r.bc.Unregister(mona.PrometheusContext{
				ClusterUID:  r.clusterUID,
				ProjectId:   "",
				Default:     false,
				IssueToken:  true,
				ClientOrgID: clientOrgId,
			})
			if err != nil {
				return ctrl.Result{}, err
			}

			err = r.bc.UnregisterPerses(mona.PrometheusContext{
				ClusterUID:  r.clusterUID,
				ProjectId:   "",
				Default:     false,
				IssueToken:  true,
				ClientOrgID: clientOrgId,
			})
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		// Authoritatively clean up everything this controller created in the client monitoring namespace before dropping the finalizer.
		monNs := clientorg.MonitoringNamespace(ns.Name)
		if err := r.kc.DeleteAllOf(ctx, &openvizapi.GrafanaDashboard{}, client.InNamespace(monNs)); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		if err := r.kc.DeleteAllOf(ctx, &openvizapi.PersesDashboard{}, client.InNamespace(monNs)); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		monNamespace := core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: monNs}}
		if err := r.kc.Delete(ctx, &monNamespace); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}

		vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &ns, func(in client.Object, createOp bool) client.Object {
			obj := in.(*core.Namespace)
			obj.ObjectMeta = core_util.RemoveFinalizer(obj.ObjectMeta, mona.PrometheusKey)

			return obj
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		klog.Infof("%s Namespace %s to remove finalizer %s", vt, ns.Name, mona.PrometheusKey)
		return ctrl.Result{}, nil
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
	if fresh.DeletionTimestamp != nil ||
		fresh.Labels[kmapi.ClientOrgKey] == "" ||
		fresh.Labels[kmapi.ClientOrgKey] == "terminating" {
		return ctrl.Result{}, nil
	}

	vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &ns, func(in client.Object, createOp bool) client.Object {
		obj := in.(*core.Namespace)
		obj.ObjectMeta = core_util.AddFinalizer(obj.ObjectMeta, mona.PrometheusKey)

		return obj
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	klog.Infof("%s Namespace %s to add finalizer %s", vt, ns.Name, mona.PrometheusKey)

	// create {client}-monitoring namespace
	monNamespace := core.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: clientorg.MonitoringNamespace(ns.Name),
		},
	}
	switch err := r.kc.Get(ctx, client.ObjectKeyFromObject(&monNamespace), &monNamespace); {
	case apierrors.IsNotFound(err):
		if err := r.kc.Create(ctx, &monNamespace); err != nil {
			return ctrl.Result{}, err
		}
	case err != nil:
		return ctrl.Result{}, err
	case monNamespace.DeletionTimestamp != nil:
		// The monitoring namespace is being torn down (teardown deleted it, while a
		// stale-cache reconcile of the still-present client-org namespace re-entered
		// this live branch). Registering backends or copying dashboards into a
		// terminating namespace is rejected by admission and just flaps create/delete.
		// Wait for it to fully delete, then recreate it cleanly on a later reconcile.
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// create client-org monitoring permission to generate grafana links
	rb := rbac.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crClientOrgMonitoring,
			Namespace: monNamespace.Name,
		},
	}
	rbvt, err := cu.CreateOrPatch(context.TODO(), r.kc, &rb, func(in client.Object, createOp bool) client.Object {
		obj := in.(*rbac.RoleBinding)

		obj.RoleRef = rbac.RoleRef{
			APIGroup: rbac.GroupName,
			Kind:     "ClusterRole",
			Name:     crClientOrgMonitoring,
		}

		obj.Subjects = []rbac.Subject{
			{
				APIGroup: rbac.GroupName,
				Kind:     "Group",
				Name:     fmt.Sprintf("ace.org.%s", clientOrgId),
			},
		}

		return obj
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	klog.Infof("%s role binding %s/%s", rbvt, rb.Namespace, rb.Name)

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
	err = r.kc.Get(context.TODO(), r.d.ServiceKey(promKey), &svcProm)
	if err != nil {
		return ctrl.Result{}, err
	}

	var rbProm rbac.RoleBinding
	if err := r.kc.Get(ctx, client.ObjectKey{Name: prometheus.CRTrickster, Namespace: svcProm.Namespace}, &rbProm); err != nil {
		return ctrl.Result{}, err
	}
	if rbProm.Annotations[prometheus.RegisteredKey] == "" {
		return ctrl.Result{}, fmt.Errorf("rolebinding %s/%s is not registered yet", rbProm.Namespace, rbProm.Name)
	}

	var pcfg mona.PrometheusConfig
	pcfg.Service = r.d.Service(promKey, &svcProm)
	// pcfg.URL = fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s:%s:%s/proxy/", r.cfg.Host, pcfg.Service.Namespace, pcfg.Service.Scheme, pcfg.Service.Name, pcfg.Service.Port)

	// remove basic auth and client cert auth
	//if rancherToken != nil {
	//	pcfg.BearerToken = rancherToken.Token
	//} else {
	pcfg.BearerToken = "" // set in b3, except for OpenShift
	// }
	pcfg.BasicAuth = mona.BasicAuth{}
	pcfg.TLS.Cert = ""
	pcfg.TLS.Key = ""
	pcfg.TLS.Ca = "" // set in b3

	if r.d.OpenShiftManaged() {
		domain, err := clustermeta.GetOpenShiftAppsDomain(r.kc)
		if err != nil {
			return ctrl.Result{}, err
		}
		pcfg.URL = fmt.Sprintf("https://%s-%s.%s", svcProm.Name, svcProm.Namespace, domain)
		pcfg.Service = mona.ServiceSpec{}
		pcfg.TLS.Ca = "" // OpenShift's default router uses a well-known CA, so no need to provide CA bundle
		pcfg.TLS.InsecureSkipTLSVerify = false

		s, err := cu.GetServiceAccountTokenSecret(r.kc, client.ObjectKey{
			Name:      prometheus.ServiceAccountTrickster,
			Namespace: promKey.Namespace,
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		pcfg.BearerToken = string(s.Data["token"])
	}

	cm, err := clustermeta.ClusterMetadata(r.kc)
	if err != nil {
		return ctrl.Result{}, err
	}
	state := cm.State()

	if r.bc != nil {
		var errs []error

		// Grafana and perses backend registration are independent, self-healing steps: each
		// re-runs when the shared marker is stale (cluster state changed) or its AppBinding is
		// missing (external deletion, or an org first registered before perses support). Errors
		// are aggregated so a hub failure on one does not block the other.
		grafanaOK, persesOK := true, true

		if monNamespace.Annotations[prometheus.RegisteredKey] != state ||
			!r.appBindingExists(ctx, monNamespace.Name, abClientOrgGrafana) {
			if err := r.registerGrafanaBackend(monNamespace.Name, pcfg, clientOrgId); err != nil {
				errs = append(errs, err)
				grafanaOK = false
			}
		}

		if monNamespace.Annotations[prometheus.RegisteredKey] != state ||
			!r.appBindingExists(ctx, monNamespace.Name, abClientOrgPerses) {
			if err := r.registerPersesBackend(monNamespace.Name, pcfg, clientOrgId); err != nil {
				errs = append(errs, err)
				persesOK = false
			}
		}

		// Stamp the marker only after BOTH backends succeed. Stamping on a perses failure would
		// leave the marker set, skipping this whole block on the next reconcile so a failed
		// perses AppBinding is never recreated.
		if grafanaOK && persesOK {
			if err := r.setNamespaceMarker(ctx, &monNamespace, state); err != nil {
				errs = append(errs, err)
			}
		}

		if len(errs) > 0 {
			return ctrl.Result{}, utilerrors.NewAggregate(errs)
		}
	}

	var errList []error

	errListGrafana, err := r.copyGrafanaDashboards(ctx, ns, monNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	errList = append(errList, errListGrafana...)

	errListPerses, err := r.copyPersesDashboards(ctx, monNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	errList = append(errList, errListPerses...)

	return ctrl.Result{}, utilerrors.NewAggregate(errList)
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
