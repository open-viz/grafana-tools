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

	"go.openviz.dev/grafana-tools/pkg/config"
	"go.openviz.dev/grafana-tools/pkg/detector"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"k8s.io/utils/ptr"
	cu "kmodules.xyz/client-go/client"
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
	inboxAPIServiceGroup    = "inbox.monitoring.appscode.com"
	amcfgName               = "alertmanager-config"
	amcfgSelectorLabelKey   = "monitoring.appscode.com/alertmanager-config-generator"
	amcfgSelectorLabelValue = "monitoring-operator"
	amcfgReceiverName       = "alertmanager-receiver"
)

// AlertmanagerReconciler reconciles an Alertmanager object
type AlertmanagerReconciler struct {
	kc         client.Client
	scheme     *runtime.Scheme
	clusterUID string
	d          detector.AlertmanagerDetector
	cfg        config.AlertmanagerConfig
}

func NewReconciler(kc client.Client, clusterUID string, d detector.AlertmanagerDetector, cfg config.AlertmanagerConfig) *AlertmanagerReconciler {
	return &AlertmanagerReconciler{
		kc:         kc,
		scheme:     kc.Scheme(),
		clusterUID: clusterUID,
		d:          d,
		cfg:        cfg,
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
	if err != nil {
		return ctrl.Result{}, err
	}

	original := am.DeepCopy()

	if am.Spec.AlertmanagerConfigSelector == nil {
		am.Spec.AlertmanagerConfigSelector = &metav1.LabelSelector{}
	}
	if am.Spec.AlertmanagerConfigSelector.MatchLabels == nil {
		am.Spec.AlertmanagerConfigSelector.MatchLabels = make(map[string]string)
	}
	am.Spec.AlertmanagerConfigSelector.MatchLabels[amcfgSelectorLabelKey] = amcfgSelectorLabelValue
	am.Spec.AlertmanagerConfigMatcherStrategy.Type = monitoringv1.NoneConfigMatcherStrategyType

	if err := r.kc.Patch(ctx, &am, client.MergeFrom(original)); err != nil {
		log.Error(err, "failed to patch Alertmanager")
		return ctrl.Result{}, err
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
			Name:      amcfgName,
			Namespace: am.Namespace,
		},
	}

	var emailConfigs []monitoringv1alpha1.EmailConfig
	if r.cfg.Email.Enabled && r.cfg.Email.To != "" && r.cfg.Email.From != "" && r.cfg.Email.Smarthost != "" && r.cfg.Email.Secret.Name != "" && r.cfg.Email.Secret.Key != "" {
		emailConfigs = append(emailConfigs, monitoringv1alpha1.EmailConfig{
			To:           r.cfg.Email.To,
			From:         r.cfg.Email.From,
			Smarthost:    r.cfg.Email.Smarthost,
			AuthUsername: r.cfg.Email.AuthUsername,
			AuthPassword: &core.SecretKeySelector{LocalObjectReference: core.LocalObjectReference{Name: r.cfg.Email.Secret.Name}, Key: r.cfg.Email.Secret.Key},
			RequireTLS:   ptr.To(r.cfg.Email.RequireTLS),
			SendResolved: ptr.To(r.cfg.Email.SendResolved),
		})
	}

	var webhookConfigs []monitoringv1alpha1.WebhookConfig
	if r.cfg.Webhook.Enabled && r.cfg.Webhook.Secret.Name != "" && r.cfg.Webhook.Secret.Key != "" {
		webhookConfigs = append(webhookConfigs, monitoringv1alpha1.WebhookConfig{
			URLSecret:    &core.SecretKeySelector{LocalObjectReference: core.LocalObjectReference{Name: r.cfg.Webhook.Secret.Name}, Key: r.cfg.Webhook.Secret.Key},
			SendResolved: ptr.To(r.cfg.Webhook.SendResolved),
			MaxAlerts:    0,
		})
	}
	if apisvc != nil {
		webhookConfigs = append(webhookConfigs, monitoringv1alpha1.WebhookConfig{
			SendResolved: ptr.To(true),
			URL:          ptr.To(fmt.Sprintf("https://%s.%s.svc:443/alerts", apisvc.Spec.Service.Name, apisvc.Spec.Service.Namespace)),
			HTTPConfig: &monitoringv1alpha1.HTTPConfig{
				TLSConfig: &monitoringv1.SafeTLSConfig{
					InsecureSkipVerify: ptr.To(true),
				},
			},
			MaxAlerts: 0,
		})
	}

	if len(emailConfigs) == 0 && len(webhookConfigs) == 0 {
		klog.Info("No valid email or webhook configuration found, deleting AlertmanagerConfig if exists")
		return r.deleteConfig(ctx, cr.Namespace, cr.Name)
	}

	crvt, err := cu.CreateOrPatch(ctx, r.kc, &cr, func(in client.Object, createOp bool) client.Object {
		obj := in.(*monitoringv1alpha1.AlertmanagerConfig)

		if obj.Labels == nil {
			obj.Labels = make(map[string]string)
		}
		obj.Labels[amcfgSelectorLabelKey] = amcfgSelectorLabelValue

		obj.Spec.Receivers = []monitoringv1alpha1.Receiver{
			{
				Name:           amcfgReceiverName,
				EmailConfigs:   emailConfigs,
				WebhookConfigs: webhookConfigs,
			},
		}

		obj.Spec.Route = &monitoringv1alpha1.Route{
			GroupBy:        []string{"job"},
			GroupWait:      "10s",
			GroupInterval:  "1m",
			Receiver:       amcfgReceiverName,
			RepeatInterval: "1h",
			Continue:       true,
		}

		return obj
	})
	if err != nil {
		return err
	}
	klog.Infof("%s AlertmanagerConfig %s", crvt, cr.Name)

	return nil
}

func (r *AlertmanagerReconciler) deleteConfig(ctx context.Context, namespace, name string) error {
	obj := &monitoringv1alpha1.AlertmanagerConfig{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	return client.IgnoreNotFound(r.kc.Delete(ctx, obj))
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
