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

package servicemonitor

import (
	"context"
	"sort"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	cu "kmodules.xyz/client-go/client"
	clustermeta "kmodules.xyz/client-go/cluster"
	meta_util "kmodules.xyz/client-go/meta"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// AutoReconciler reconciles a ServiceMonitor object
type AutoReconciler struct {
	cfg    *rest.Config
	kc     client.Client
	scheme *runtime.Scheme
}

func NewAutoReconciler(cfg *rest.Config, kc client.Client) *AutoReconciler {
	return &AutoReconciler{
		cfg:    cfg,
		kc:     kc,
		scheme: kc.Scheme(),
	}
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ServiceMonitor object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.0/pkg/reconcile
func (r *AutoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var svcMon monitoringv1.ServiceMonitor
	if err := r.kc.Get(ctx, req.NamespacedName, &svcMon); err != nil {
		log.Error(err, "unable to fetch ServiceMonitor")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// has federate label
	val, found := svcMon.Labels[mona.PrometheusKey]
	if !found || val != mona.PrometheusValueAuto {
		return ctrl.Result{}, nil
	}

	var promList monitoringv1.PrometheusList
	if err := r.kc.List(context.TODO(), &promList); err != nil {
		log.Error(err, "unable to list Prometheus")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	prometheuses := promList.Items
	if len(prometheuses) == 0 {
		return ctrl.Result{}, nil
	}
	if prom, err := HasProm(r.kc, &svcMon, prometheuses); err != nil || prom != nil {
		return ctrl.Result{}, err
	}

	sort.Slice(prometheuses, func(i, j int) bool {
		if prometheuses[i].Namespace != prometheuses[j].Namespace {
			return prometheuses[i].Namespace < prometheuses[j].Namespace
		}
		return prometheuses[i].Name < prometheuses[j].Name
	})

	if !clustermeta.IsRancherManaged(r.kc.RESTMapper()) {
		// non rancher
		err := r.updateServiceMonitorLabels(prometheuses[0], &svcMon)
		if err != nil {
			log.Error(err, "failed to apply label")
		}
		return ctrl.Result{}, err
	}

	sysProjectId, _, _ := clustermeta.GetSystemProjectId(r.kc)

	// rancher
	projectId, found, err := clustermeta.GetProjectId(r.kc, svcMon.Namespace)
	if err != nil {
		log.Error(err, "unable to detect projectId")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// if not in a project or in system project, use System Prometheus
	if !found || projectId == sysProjectId {
		// find system prometheus and use that
		for _, prom := range prometheuses {
			if prom.Namespace == clustermeta.RancherMonitoringNamespace {
				err = r.updateServiceMonitorLabels(prom, &svcMon)
				if err != nil {
					log.Error(err, "failed to apply label")
				}
				return ctrl.Result{}, err
			}
		}
	}

	// find project prometheus
	var ns core.Namespace
	if err := r.kc.Get(context.TODO(), client.ObjectKey{Name: svcMon.Namespace}, &ns); err != nil {
		log.Error(err, "failed to get namespace", "ns", svcMon.Namespace)
		return ctrl.Result{}, err
	}
	helmProjectId := ns.Labels[clustermeta.LabelKeyRancherHelmProjectId]
	if helmProjectId == "" {
		log.Info("project prometheus is not deployed")
		return ctrl.Result{}, nil
	}

	for _, prom := range prometheuses {
		if prom.Namespace == clustermeta.RancherMonitoringNamespace {
			continue
		}

		sel, err := metav1.LabelSelectorAsSelector(prom.Spec.ServiceMonitorNamespaceSelector)
		if err != nil {
			return ctrl.Result{}, err
		}
		if sel.Matches(labels.Set{clustermeta.LabelKeyRancherHelmProjectId: helmProjectId}) {
			err := r.updateServiceMonitorLabels(prom, &svcMon)
			if err != nil {
				log.Error(err, "failed to apply label")
			}
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func HasProm(kc client.Client, svcmon *monitoringv1.ServiceMonitor, prometheuses []*monitoringv1.Prometheus) (*monitoringv1.Prometheus, error) {
	var ns core.Namespace
	err := kc.Get(context.TODO(), client.ObjectKey{Name: svcmon.Namespace}, &ns)
	if err != nil {
		return nil, err
	}

	for _, prom := range prometheuses {
		nsSelector, err := metav1.LabelSelectorAsSelector(prom.Spec.ServiceMonitorNamespaceSelector)
		if err != nil {
			return nil, err
		}
		if !nsSelector.Matches(labels.Set(ns.Labels)) {
			continue
		}

		sel, err := metav1.LabelSelectorAsSelector(prom.Spec.ServiceMonitorSelector)
		if err != nil {
			return nil, err
		}
		if sel.Matches(labels.Set(svcmon.Labels)) {
			return prom, nil
		}
	}
	return nil, nil
}

func (r *AutoReconciler) updateServiceMonitorLabels(prom *monitoringv1.Prometheus, src *monitoringv1.ServiceMonitor) error {
	vt, err := cu.CreateOrPatch(context.TODO(), r.kc, src, func(in client.Object, createOp bool) client.Object {
		obj := in.(*monitoringv1.ServiceMonitor)

		labels, _ := meta_util.LabelsForLabelSelector(prom.Spec.ServiceMonitorSelector)
		obj.Labels = meta_util.OverwriteKeys(obj.Labels, labels)

		return obj
	})
	if err != nil {
		return err
	}
	klog.Infof("%s ServiceMonitor %s/%s", vt, src.Namespace, src.Name)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AutoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("servicemonitor-auto").
		For(&monitoringv1.ServiceMonitor{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			val, found := obj.GetLabels()[mona.PrometheusKey]
			return found && val == mona.PrometheusValueAuto
		}))).
		Watches(
			&monitoringv1.Prometheus{},
			handler.EnqueueRequestsFromMapFunc(ServiceMonitorsForPrometheus(r.kc, map[string]string{
				mona.PrometheusKey: mona.PrometheusValueAuto,
			}))).
		Complete(r)
}
