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

package alertmanager

import (
	"context"
	"fmt"

	"go.openviz.dev/grafana-tools/pkg/detector"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"k8s.io/utils/ptr"
	cu "kmodules.xyz/client-go/client"
	meta_util "kmodules.xyz/client-go/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// v1alpha1.inbox.monitoring.appscode.com       monitoring/inbox-agent
// Grant permission to alertmanager to call webhook

const (
	inboxAPIServiceGroup = "inbox.monitoring.appscode.com"
	amcfgInboxAgent      = "inbox-agent"
	amcfgLabelKey        = "inbox-agent-amcfg-label"
	amcfgLabelValue      = "inbox-agent-amcfg"
)

var selfNamespace = meta_util.PodNamespace()

var defaultPresetsLabels = map[string]string{
	"charts.x-helm.dev/is-default-preset": "true",
}

// AlertmanagerReconciler reconciles an Alertmanager object
type AlertmanagerReconciler struct {
	kc         client.Client
	scheme     *runtime.Scheme
	clusterUID string
	d          detector.AlertmanagerDetector
}

func NewReconciler(kc client.Client, clusterUID string, d detector.AlertmanagerDetector) *AlertmanagerReconciler {
	return &AlertmanagerReconciler{
		kc:         kc,
		scheme:     kc.Scheme(),
		clusterUID: clusterUID,
		d:          d,
	}
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Alertmanager object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.0/pkg/reconcile
func (r *AlertmanagerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var am monitoringv1.Alertmanager
	if err := r.kc.Get(ctx, req.NamespacedName, &am); err != nil {
		log.Error(err, "unable to fetch Alertmanager")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if ready, err := r.d.Ready(); !ready {
		return ctrl.Result{}, err
	}

	if am.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}

	apisvc, err := r.GetInboxAPIService(ctx)
	if err != nil || apisvc == nil {
		return ctrl.Result{}, err
	}

	if am.Spec.AlertmanagerConfigSelector == nil {
		am.Spec.AlertmanagerConfigSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{amcfgLabelKey: amcfgLabelValue},
		}
	} else {
		am.Spec.AlertmanagerConfigSelector.MatchLabels[amcfgLabelKey] = amcfgLabelValue
	}

	if err := r.SetupClusterForAlertmanager(ctx, &am, apisvc); err != nil {
		log.Error(err, "unable to setup Alertmanager config")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AlertmanagerReconciler) SetupClusterForAlertmanager(ctx context.Context, am *monitoringv1.Alertmanager, apisvc *apiregistrationv1.APIService) error {
	cr := monitoringv1alpha1.AlertmanagerConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      amcfgInboxAgent,
			Namespace: am.Namespace,
		},
	}
	crvt, err := cu.CreateOrPatch(ctx, r.kc, &cr, func(in client.Object, createOp bool) client.Object {
		obj := in.(*monitoringv1alpha1.AlertmanagerConfig)

		obj.Labels[amcfgLabelKey] = amcfgLabelValue

		obj.Spec.Receivers = []monitoringv1alpha1.Receiver{
			{
				Name: "webhook",
				WebhookConfigs: []monitoringv1alpha1.WebhookConfig{
					{
						SendResolved: ptr.To(true),
						URL:          ptr.To(fmt.Sprintf("https://%s.%s.svc:443/alerts", apisvc.Spec.Service.Name, apisvc.Spec.Service.Namespace)),
						HTTPConfig: &monitoringv1alpha1.HTTPConfig{
							TLSConfig: &monitoringv1.SafeTLSConfig{
								InsecureSkipVerify: ptr.To(true),
							},
						},
						MaxAlerts: 0,
					},
				},
			},
		}

		obj.Spec.Route = &monitoringv1alpha1.Route{
			GroupBy:        []string{"job"},
			GroupWait:      "10s",
			GroupInterval:  "1m",
			Receiver:       "webhook",
			RepeatInterval: "1h",
		}

		return obj
	})
	if err != nil {
		return err
	}
	klog.Infof("%s AlertmanagerConfig %s", crvt, cr.Name)

	return nil
}

func (r *AlertmanagerReconciler) GetInboxAPIService(ctx context.Context) (*apiregistrationv1.APIService, error) {
	var list apiregistrationv1.APIServiceList
	err := r.kc.List(ctx, &list)
	if err != nil {
		return nil, err
	}
	for _, apisvc := range list.Items {
		if apisvc.Spec.Group == inboxAPIServiceGroup {
			return &apisvc, nil
		}
	}
	return nil, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AlertmanagerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	stateHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []reconcile.Request {
		var amList monitoringv1.AlertmanagerList
		err := r.kc.List(ctx, &amList)
		if err != nil {
			return nil
		}

		var req []reconcile.Request
		for _, am := range amList.Items {
			req = append(req, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&am)})
		}
		return req
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&monitoringv1.Alertmanager{}).
		Watches(&apiregistrationv1.APIService{}, stateHandler, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			apisvc := obj.(*apiregistrationv1.APIService)
			return apisvc.Spec.Group == inboxAPIServiceGroup
		}))).
		Complete(r)
}
