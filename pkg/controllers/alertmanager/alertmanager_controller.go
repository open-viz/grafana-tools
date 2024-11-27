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

	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/pkg/detector"

	"github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringv1beta1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1beta1"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/klog/v2"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"k8s.io/utils/ptr"
	kutil "kmodules.xyz/client-go"
	kmapi "kmodules.xyz/client-go/api/v1"
	cu "kmodules.xyz/client-go/client"
	clustermeta "kmodules.xyz/client-go/cluster"
	meta_util "kmodules.xyz/client-go/meta"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	chartsapi "x-helm.dev/apimachinery/apis/charts/v1alpha1"
)

// v1alpha1.inbox.monitoring.appscode.com       monitoring/inbox-agent
// Grant permission to alertmanager to call webhook

const (
	portAlertmanager = "http-web"
	saInboxAgent     = "inbox-agent"
	crInboxAgent     = "appscode:inbox-agent:webhook"

	registeredKey          = mona.GroupName + "/registered"
	presetsMonitoring      = "monitoring-presets"
	appBindingAlertmanager = "default-alertmanager"
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

	key := req.NamespacedName
	isDefault := r.d.IsDefault(key)

	if am.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}

	if err := r.SetupClusterForAlertmanager(ctx, &am, isDefault); err != nil {
		log.Error(err, "unable to setup Alertmanager config")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AlertmanagerReconciler) SetupClusterForAlertmanager(ctx context.Context, am *monitoringv1.Alertmanager, isDefault bool) error {
	key := client.ObjectKeyFromObject(am)

	cm, err := clustermeta.ClusterMetadata(r.kc)
	if err != nil {
		return err
	}
	state := cm.State()

	cr := rbac.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: crInboxAgent,
		},
	}
	crvt, err := cu.CreateOrPatch(context.TODO(), r.kc, &cr, func(in client.Object, createOp bool) client.Object {
		obj := in.(*rbac.ClusterRole)

		obj.Rules = []rbac.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"services/proxy"},
				Verbs:     []string{"*"},
			},
		}

		return obj
	})
	if err != nil {
		return err
	}
	klog.Infof("%s ClusterRole %s", crvt, cr.Name)

	rb := rbac.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crInboxAgent,
			Namespace: key.Namespace,
		},
	}
	rbvt, err := cu.CreateOrPatch(context.TODO(), r.kc, &rb, func(in client.Object, createOp bool) client.Object {
		obj := in.(*rbac.RoleBinding)
		ref := metav1.NewControllerRef(am, schema.GroupVersionKind{
			Group:   monitoring.GroupName,
			Version: monitoringv1beta1.Version,
			Kind:    "Alertmanager",
		})
		obj.OwnerReferences = []metav1.OwnerReference{*ref}

		obj.RoleRef = rbac.RoleRef{
			APIGroup: rbac.GroupName,
			Kind:     "ClusterRole",
			Name:     cr.Name,
		}

		obj.Subjects = []rbac.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saInboxAgent,
				Namespace: key.Namespace,
			},
		}

		return obj
	})
	if err != nil {
		return err
	}
	klog.Infof("%s role binding %s/%s", rbvt, rb.Namespace, rb.Name)

	err = r.CreatePreset(am, isDefault)
	if err != nil {
		return err
	}

	applyMarkers := func(in client.Object, createOp bool) client.Object {
		obj := in.(*rbac.RoleBinding)
		if obj.Annotations == nil {
			obj.Annotations = map[string]string{}
		}
		obj.Annotations[registeredKey] = state
		return obj
	}

	// fix legacy deployments
	if rb.Annotations[registeredKey] != state {
		rbvt, err = cu.CreateOrPatch(context.TODO(), r.kc, &rb, applyMarkers)
		if err != nil {
			return err
		}
		klog.Infof("%s rolebinding %s/%s with %s annotation", rbvt, rb.Namespace, rb.Name, registeredKey)
	}

	return nil
}

func (r *AlertmanagerReconciler) CreatePreset(p *monitoringv1.Alertmanager, isDefault bool) error {
	presets := r.GeneratePresetForAlertmanager(*p)
	presetBytes, err := json.Marshal(presets)
	if err != nil {
		return err
	}

	if r.d.Federated() && !isDefault {
		return r.CreateProjectPreset(p, presetBytes)
	}
	return r.CreateClusterPreset(presetBytes)
}

func (r *AlertmanagerReconciler) NamespaceForProjectSettings(prom *monitoringv1.Alertmanager) (ns string, projectId string, err error) {
	if prom.Namespace == clustermeta.RancherMonitoringNamespace &&
		prom.Name == clustermeta.RancherMonitoringAlertmanager {
		var found bool
		projectId, found, err = clustermeta.GetSystemProjectId(r.kc)
		if err != nil {
			return
		} else if !found {
			err = fmt.Errorf("failed to detect system projectId for Alertmanager %s/%s", prom.Namespace, prom.Name)
			return
		}
	} else {
		ls := prom.Spec.ServiceMonitorNamespaceSelector
		if ls.MatchLabels == nil {
			ls.MatchLabels = make(map[string]string)
		}
		projectId = ls.MatchLabels[clustermeta.LabelKeyRancherHelmProjectId]
		if projectId == "" {
			err = fmt.Errorf("expected %s label in Alertmanager %s/%s  spec.serviceMonitorNamespaceSelector",
				clustermeta.LabelKeyRancherHelmProjectId, prom.Namespace, prom.Name)
			return
		}
	}
	ns = fmt.Sprintf("cattle-project-%s", projectId)
	return
}

func (r *AlertmanagerReconciler) CreateClusterPreset(presetBytes []byte) error {
	ccp := chartsapi.ClusterChartPreset{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: presetsMonitoring,
		},
	}
	vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &ccp, func(in client.Object, createOp bool) client.Object {
		obj := in.(*chartsapi.ClusterChartPreset)

		obj.Labels = defaultPresetsLabels
		obj.Spec = chartsapi.ClusterChartPresetSpec{
			Values: &runtime.RawExtension{
				Raw: presetBytes,
			},
		}

		return obj
	})
	if err != nil {
		return err
	}
	klog.Infof("%s ClusterChartPreset %s", vt, ccp.Name)
	return nil
}

func (r *AlertmanagerReconciler) CreateProjectPreset(p *monitoringv1.Alertmanager, presetBytes []byte) error {
	ns, _, err := r.NamespaceForProjectSettings(p)
	if err != nil {
		return err
	}

	cp := chartsapi.ChartPreset{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      presetsMonitoring,
			Namespace: ns,
		},
	}
	vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &cp, func(in client.Object, createOp bool) client.Object {
		obj := in.(*chartsapi.ChartPreset)

		obj.Labels = defaultPresetsLabels
		obj.Spec = chartsapi.ClusterChartPresetSpec{
			Values: &runtime.RawExtension{
				Raw: presetBytes,
			},
		}

		return obj
	})
	if err != nil {
		return err
	}
	klog.Infof("%s ChartPreset %s/%s", vt, cp.Namespace, cp.Name)
	return nil
}

func (r *AlertmanagerReconciler) GeneratePresetForAlertmanager(p monitoringv1.Alertmanager) mona.MonitoringPresets {
	var preset mona.MonitoringPresets

	preset.Spec.Monitoring.Agent = string(mona.AgentAlertmanagerOperator)
	svcmonLabels, ok := meta_util.LabelsForLabelSelector(p.Spec.ServiceMonitorSelector)
	if !ok {
		klog.Warningf("Alertmanager %s/%s uses match expressions in ServiceMonitorSelector", p.Namespace, p.Name)
	}
	preset.Spec.Monitoring.ServiceMonitor.Labels = svcmonLabels

	preset.Form.Alert.Enabled = mona.SeverityFlagCritical
	ruleLabels, ok := meta_util.LabelsForLabelSelector(p.Spec.RuleSelector)
	if !ok {
		klog.Warningf("Alertmanager %s/%s uses match expressions in RuleSelector", p.Namespace, p.Name)
	}
	preset.Form.Alert.Labels = ruleLabels

	return preset
}

func (r *AlertmanagerReconciler) CreateAlertmanagerAppBinding(prom *monitoringv1.Alertmanager, svc *core.Service) (kutil.VerbType, error) {
	ab := appcatalog.AppBinding{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      appBindingAlertmanager,
			Namespace: prom.Namespace,
		},
	}

	vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &ab, func(in client.Object, createOp bool) client.Object {
		obj := in.(*appcatalog.AppBinding)

		ref := metav1.NewControllerRef(prom, schema.GroupVersionKind{
			Group:   monitoring.GroupName,
			Version: monitoringv1beta1.Version,
			Kind:    "Alertmanager",
		})
		obj.OwnerReferences = []metav1.OwnerReference{*ref}

		if obj.Annotations == nil {
			obj.Annotations = make(map[string]string)
		}
		obj.Annotations["monitoring.appscode.com/is-default-alertmanager"] = "true"

		obj.Spec.Type = "Alertmanager"
		obj.Spec.AppRef = &kmapi.TypedObjectReference{
			APIGroup:  monitoring.GroupName,
			Kind:      "Alertmanager",
			Namespace: prom.Namespace,
			Name:      prom.Name,
		}
		obj.Spec.ClientConfig = appcatalog.ClientConfig{
			// URL:                   nil,
			Service: &appcatalog.ServiceReference{
				Scheme:    "http",
				Namespace: svc.Namespace,
				Name:      svc.Name,
				Port:      0,
				Path:      "",
				Query:     "",
			},
			// InsecureSkipTLSVerify: false,
			// CABundle:              nil,
			// ServerName:            "",
		}
		for _, p := range svc.Spec.Ports {
			if p.Name == portAlertmanager {
				obj.Spec.ClientConfig.Service.Port = p.Port
			}
		}

		return obj
	})
	if err == nil {
		klog.Infof("%s AppBinding %s/%s", vt, ab.Namespace, ab.Name)
	}
	return vt, err
}

func (r *AlertmanagerReconciler) CreateGrafanaAppBinding(prom *monitoringv1.Alertmanager, resp *GrafanaDatasourceResponse) error {
	ab := appcatalog.AppBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appBindingGrafana,
			Namespace: prom.Namespace,
		},
	}

	abvt, err := cu.CreateOrPatch(context.TODO(), r.kc, &ab, func(in client.Object, createOp bool) client.Object {
		obj := in.(*appcatalog.AppBinding)

		ref := metav1.NewControllerRef(prom, schema.GroupVersionKind{
			Group:   monitoring.GroupName,
			Version: monitoringv1beta1.Version,
			Kind:    "Alertmanager",
		})
		obj.OwnerReferences = []metav1.OwnerReference{*ref}

		if obj.Annotations == nil {
			obj.Annotations = make(map[string]string)
		}
		obj.Annotations["monitoring.appscode.com/is-default-grafana"] = "true"

		obj.Spec.Type = "Grafana"
		obj.Spec.AppRef = nil
		obj.Spec.ClientConfig = appcatalog.ClientConfig{
			URL: ptr.To(resp.Grafana.URL),
			//Service: &appcatalog.ServiceReference{
			//	Scheme:    "http",
			//	Namespace: svc.Namespace,
			//	Name:      svc.Name,
			//	Port:      0,
			//	Path:      "",
			//	Query:     "",
			//},
			//InsecureSkipTLSVerify: false,
			//CABundle:              nil,
			//ServerName:            "",
		}
		obj.Spec.Secret = &core.LocalObjectReference{
			Name: ab.Name + "-auth",
		}

		// TODO: handle TLS config returned in resp
		if caCert := r.bc.CACert(); len(caCert) > 0 {
			obj.Spec.ClientConfig.CABundle = caCert
		}

		params := openvizapi.GrafanaConfiguration{
			TypeMeta: metav1.TypeMeta{
				Kind:       "GrafanaConfiguration",
				APIVersion: openvizapi.SchemeGroupVersion.String(),
			},
			Datasource: resp.Datasource,
			FolderID:   resp.FolderID,
		}
		paramBytes, err := json.Marshal(params)
		if err != nil {
			panic(err)
		}
		obj.Spec.Parameters = &runtime.RawExtension{
			Raw: paramBytes,
		}

		return obj
	})
	if err == nil {
		klog.Infof("%s AppBinding %s/%s", abvt, ab.Namespace, ab.Name)

		authSecret := core.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ab.Name + "-auth",
				Namespace: prom.Namespace,
			},
		}

		svt, e2 := cu.CreateOrPatch(context.TODO(), r.kc, &authSecret, func(in client.Object, createOp bool) client.Object {
			obj := in.(*core.Secret)

			ref := metav1.NewControllerRef(&ab, schema.GroupVersionKind{
				Group:   appcatalog.SchemeGroupVersion.Group,
				Version: appcatalog.SchemeGroupVersion.Version,
				Kind:    "AppBinding",
			})
			obj.OwnerReferences = []metav1.OwnerReference{*ref}

			obj.StringData = map[string]string{
				"token": resp.Grafana.BearerToken,
			}

			return obj
		})
		if e2 == nil {
			klog.Infof("%s Grafana auth secret %s/%s", svt, authSecret.Namespace, authSecret.Name)
		}
	}

	return err
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
			return apisvc.Spec.Group == "inbox.monitoring.appscode.com"
		}))).
		Complete(r)
}
