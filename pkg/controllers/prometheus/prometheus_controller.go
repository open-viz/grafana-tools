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
	"fmt"

	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/pkg/detector"
	"go.openviz.dev/grafana-tools/pkg/rancherutil"

	"github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	kutil "kmodules.xyz/client-go"
	kmapi "kmodules.xyz/client-go/api/v1"
	cu "kmodules.xyz/client-go/client"
	clustermeta "kmodules.xyz/client-go/cluster"
	core_util "kmodules.xyz/client-go/core/v1"
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

const (
	ServiceAccountTrickster = "trickster"
	CRTrickster             = "appscode:trickster:proxy"

	RegisteredKey        = mona.GroupName + "/registered"
	tokenIDKey           = mona.GroupName + "/token-id"
	presetsMonitoring    = "monitoring-presets"
	appBindingPrometheus = "default-prometheus"
	appBindingGrafana    = "default-grafana"

	clusterLabelKey    = "cluster"
	clusterUIDLabelKey = "cluster_uid"
)

var (
	selfNamespace        = meta_util.PodNamespace()
	defaultPresetsLabels = map[string]string{
		"charts.x-helm.dev/is-default-preset": "true",
	}
	defaultPrometheusStackLabels = map[string]string{
		"release": "kube-prometheus-stack",
	}
	defaultRancherMonitoringLabels = map[string]string{
		"release": "rancher-monitoring",
	}
)

// PrometheusReconciler reconciles a Prometheus object
type PrometheusReconciler struct {
	kc                    client.Client
	scheme                *runtime.Scheme
	bc                    *Client
	clusterUID            string
	hubUID                string
	rancherAuthSecretName string
	d                     detector.PrometheusDetector
}

func NewReconciler(kc client.Client, bc *Client, clusterUID, hubUID, rancherAuthSecretName string, d detector.PrometheusDetector) *PrometheusReconciler {
	return &PrometheusReconciler{
		kc:                    kc,
		scheme:                kc.Scheme(),
		bc:                    bc,
		clusterUID:            clusterUID,
		hubUID:                hubUID,
		rancherAuthSecretName: rancherAuthSecretName,
		d:                     d,
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

	if ready, err := r.d.Ready(); !ready {
		return ctrl.Result{}, err
	}

	key := req.NamespacedName
	isDefault := r.d.IsDefault(key)

	// do nothing for cluster prometheus in OpenShift
	if r.d.OpenShiftManaged() && !isDefault {
		return ctrl.Result{}, nil
	}

	if prom.DeletionTimestamp != nil {
		err := r.CleanupPreset(&prom, isDefault)
		if err != nil {
			return ctrl.Result{}, err
		}

		var projectId string
		if !isDefault {
			_, projectId, err = r.NamespaceForProjectSettings(&prom)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
		if r.bc != nil {
			err := r.bc.Unregister(mona.PrometheusContext{
				ClusterUID: r.clusterUID,
				ProjectId:  projectId,
				Default:    isDefault,
			})
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &prom, func(in client.Object, createOp bool) client.Object {
			obj := in.(*monitoringv1.Prometheus)
			obj.ObjectMeta = core_util.RemoveFinalizer(obj.ObjectMeta, mona.PrometheusKey)

			return obj
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		klog.Infof("%s Prometheus %s/%s to remove finalizer %s", vt, prom.Namespace, prom.Name, mona.PrometheusKey)
		return ctrl.Result{}, nil
	}

	if err := r.ensureExternalLabels(ctx, &prom); err != nil {
		log.Error(err, "unable to ensure Prometheus external labels")
		return ctrl.Result{}, err
	}

	vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &prom, func(in client.Object, createOp bool) client.Object {
		obj := in.(*monitoringv1.Prometheus)
		obj.ObjectMeta = core_util.AddFinalizer(obj.ObjectMeta, mona.PrometheusKey)

		return obj
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	klog.Infof("%s Prometheus %s/%s to add finalizer %s", vt, prom.Namespace, prom.Name, mona.PrometheusKey)

	if err := r.SetupClusterForPrometheus(ctx, &prom, isDefault); err != nil {
		log.Error(err, "unable to setup Prometheus")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *PrometheusReconciler) ensureExternalLabels(ctx context.Context, prom *monitoringv1.Prometheus) error {
	cm, err := clustermeta.ClusterMetadata(r.kc)
	if err != nil {
		return err
	}

	existingName, nameExists := prom.Spec.ExternalLabels[clusterLabelKey]
	existingUID, uidExists := prom.Spec.ExternalLabels[clusterUIDLabelKey]
	if nameExists && existingName == cm.Name && uidExists && existingUID == cm.UID {
		return nil
	}

	original := prom.DeepCopy()
	if prom.Spec.ExternalLabels == nil {
		prom.Spec.ExternalLabels = make(map[string]string)
	}
	prom.Spec.ExternalLabels[clusterLabelKey] = cm.Name
	prom.Spec.ExternalLabels[clusterUIDLabelKey] = cm.UID
	return r.kc.Patch(ctx, prom, client.MergeFrom(original))
}

func (r *PrometheusReconciler) findServiceForPrometheus(prom types.NamespacedName) (*core.Service, error) {
	var svc core.Service
	err := r.kc.Get(context.TODO(), r.d.ServiceKey(prom), &svc)
	if err != nil {
		return nil, err
	}
	return &svc, nil
}

func (r *PrometheusReconciler) SetupClusterForPrometheus(ctx context.Context, prom *monitoringv1.Prometheus, isDefault bool) error {
	key := client.ObjectKeyFromObject(prom)

	svcProm, err := r.findServiceForPrometheus(key)
	if err != nil {
		return err
	}

	cm, err := clustermeta.ClusterMetadata(r.kc)
	if err != nil {
		return err
	}
	state := cm.State()

	var rancherToken *rancherutil.RancherToken
	var saToken string
	var caCrt string
	if r.d.RancherManaged() && r.rancherAuthSecretName != "" {
		var rancherSecret core.Secret
		rancherSecretKey := client.ObjectKey{Name: r.rancherAuthSecretName, Namespace: selfNamespace}
		err = r.kc.Get(context.TODO(), rancherSecretKey, &rancherSecret)
		if err != nil {
			return err
		}

		_, rancherToken, err = rancherutil.FindToken(ctx, &rancherSecret, state)
		if err != nil {
			return err
		}
		caCrt = string(rancherSecret.Data["ca.crt"])
	} else if !clustermeta.IsACEManagedSpoke(r.kc) || r.d.OpenShiftManaged() {
		sa := core.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ServiceAccountTrickster,
				Namespace: key.Namespace,
			},
		}
		savt, err := cu.CreateOrPatch(context.TODO(), r.kc, &sa, func(in client.Object, createOp bool) client.Object {
			obj := in.(*core.ServiceAccount)
			ref := metav1.NewControllerRef(prom, schema.GroupVersionKind{
				Group:   monitoring.GroupName,
				Version: monitoringv1.Version,
				Kind:    "Prometheus",
			})
			obj.OwnerReferences = []metav1.OwnerReference{*ref}

			return obj
		})
		if err != nil {
			return err
		}
		klog.Infof("%s service account %s/%s", savt, sa.Namespace, sa.Name)

		s, err := cu.GetServiceAccountTokenSecret(r.kc, client.ObjectKeyFromObject(&sa))
		if err != nil {
			return err
		}
		saToken = string(s.Data["token"])
		caCrt = string(s.Data["ca.crt"])
	}

	cr := rbac.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: CRTrickster,
		},
	}
	if !r.d.OpenShiftManaged() {
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
	}

	rb := rbac.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CRTrickster,
			Namespace: svcProm.Namespace,
		},
	}
	rbvt, err := cu.CreateOrPatch(context.TODO(), r.kc, &rb, func(in client.Object, createOp bool) client.Object {
		obj := in.(*rbac.RoleBinding)
		ref := metav1.NewControllerRef(svcProm, schema.GroupVersionKind{
			Group:   "",
			Version: "v1",
			Kind:    "Service",
		})
		obj.OwnerReferences = []metav1.OwnerReference{*ref}

		obj.RoleRef = rbac.RoleRef{
			APIGroup: rbac.GroupName,
			Kind:     "ClusterRole",
			Name:     If(r.d.OpenShiftManaged(), "cluster-monitoring-view", cr.Name),
		}

		if rancherToken != nil {
			obj.Subjects = []rbac.Subject{
				{
					Kind: "User",
					Name: rancherToken.UserID,
				},
			}
		} else {
			if clustermeta.IsACEManagedSpoke(r.kc) && !r.d.OpenShiftManaged() {
				obj.Subjects = []rbac.Subject{
					{
						Kind:      "ServiceAccount",
						Name:      ServiceAccountTrickster,
						Namespace: "monitoring", // ocm spokes use sa in "monitoring" namespace
					},
				}
			} else {
				obj.Subjects = []rbac.Subject{
					{
						Kind:      "ServiceAccount",
						Name:      ServiceAccountTrickster,
						Namespace: key.Namespace,
					},
				}
			}
		}

		return obj
	})
	if err != nil {
		return err
	}
	klog.Infof("%s role binding %s/%s", rbvt, rb.Namespace, rb.Name)

	err = r.CreatePreset(prom, isDefault)
	if err != nil {
		return err
	}

	pcfgSvc := r.d.Service(key, svcProm)

	var pcfg mona.PrometheusConfig
	pcfg.Service = pcfgSvc
	// pcfg.URL = fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s:%s:%s/proxy/", r.cfg.Host, pcfg.Service.Namespace, pcfg.Service.Scheme, pcfg.Service.Name, pcfg.Service.Port)

	// remove basic auth and client cert auth
	if rancherToken != nil {
		pcfg.BearerToken = rancherToken.Token
	} else {
		pcfg.BearerToken = saToken
	}
	pcfg.BasicAuth = mona.BasicAuth{}
	pcfg.TLS.Cert = ""
	pcfg.TLS.Key = ""
	pcfg.TLS.Ca = caCrt

	if r.d.OpenShiftManaged() {
		domain, err := clustermeta.GetOpenShiftAppsDomain(r.kc)
		if err != nil {
			return err
		}
		pcfg.URL = fmt.Sprintf("https://%s-%s.%s", svcProm.Name, svcProm.Namespace, domain)
		pcfg.Service = mona.ServiceSpec{}
		pcfg.TLS.Ca = "" // OpenShift's default router uses a well-known CA, so no need to provide CA bundle
		pcfg.TLS.InsecureSkipTLSVerify = false
	}

	applyMarkers := func(in client.Object, createOp bool) client.Object {
		obj := in.(*rbac.RoleBinding)
		if obj.Annotations == nil {
			obj.Annotations = map[string]string{}
		}
		obj.Annotations[RegisteredKey] = state
		if rancherToken != nil {
			obj.Annotations[tokenIDKey] = rancherToken.TokenID
		} else {
			delete(obj.Annotations, tokenIDKey)
		}
		return obj
	}

	// fix legacy deployments
	if rb.Annotations[RegisteredKey] == "true" {
		rbvt, err = cu.CreateOrPatch(context.TODO(), r.kc, &rb, applyMarkers)
		if err != nil {
			return err
		}
		klog.Infof("%s rolebinding %s/%s with %s annotation", rbvt, rb.Namespace, rb.Name, RegisteredKey)

		return nil
	} else if r.bc != nil &&
		(rb.Annotations[RegisteredKey] != state ||
			(rancherToken != nil && rb.Annotations[tokenIDKey] != rancherToken.TokenID)) {
		var projectId string
		if !isDefault {
			_, projectId, err = r.NamespaceForProjectSettings(prom)
			if err != nil {
				return err
			}
		}
		resp, err := r.bc.Register(mona.PrometheusContext{
			HubUID:     r.hubUID,
			ClusterUID: r.clusterUID,
			ProjectId:  projectId,
			Default:    isDefault,
		}, pcfg)
		if err != nil {
			return err
		}

		if isDefault {
			_, err := r.CreatePrometheusAppBinding(prom, &pcfgSvc)
			if err != nil {
				return err
			}

			err = r.CreateGrafanaAppBinding(prom, resp)
			if err != nil {
				return err
			}
		}

		rbvt, err = cu.CreateOrPatch(context.TODO(), r.kc, &rb, applyMarkers)
		if err != nil {
			return err
		}
		klog.Infof("%s rolebinding %s/%s with %s annotation", rbvt, rb.Namespace, rb.Name, RegisteredKey)
	}

	return nil
}

func (r *PrometheusReconciler) CreatePreset(p *monitoringv1.Prometheus, isDefault bool) error {
	presets := r.GeneratePresetForPrometheus(*p, isDefault)
	presetBytes, err := json.Marshal(presets)
	if err != nil {
		return err
	}

	if r.d.Federated() && !isDefault {
		return r.CreateProjectPreset(p, presetBytes)
	}
	return r.CreateClusterPreset(presetBytes)
}

func (r *PrometheusReconciler) CleanupPreset(p *monitoringv1.Prometheus, isDefault bool) error {
	if r.d.Federated() && !isDefault {
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
		err = r.kc.Delete(context.TODO(), &cp)
		if err != nil {
			return err
		}
		klog.Infof("deleted ChartPreset %s/%s", cp.Namespace, cp.Name)
		return nil
	}

	ccp := chartsapi.ClusterChartPreset{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: presetsMonitoring,
		},
	}
	err := r.kc.Delete(context.TODO(), &ccp)
	if err != nil {
		return err
	}
	klog.Infof("deleted ClusterChartPreset %s", ccp.Name)
	return nil
}

func (r *PrometheusReconciler) NamespaceForProjectSettings(prom *monitoringv1.Prometheus) (ns string, projectId string, err error) {
	if prom.Namespace == clustermeta.RancherMonitoringNamespace &&
		prom.Name == clustermeta.RancherMonitoringPrometheus {
		var found bool
		projectId, found, err = clustermeta.GetSystemProjectId(r.kc)
		if err != nil {
			return
		} else if !found {
			err = fmt.Errorf("failed to detect system projectId for Prometheus %s/%s", prom.Namespace, prom.Name)
			return
		}
	} else {
		ls := prom.Spec.ServiceMonitorNamespaceSelector
		if ls.MatchLabels == nil {
			ls.MatchLabels = make(map[string]string)
		}
		projectId = ls.MatchLabels[clustermeta.LabelKeyRancherHelmProjectId]
		if projectId == "" {
			err = fmt.Errorf("expected %s label in Prometheus %s/%s  spec.serviceMonitorNamespaceSelector",
				clustermeta.LabelKeyRancherHelmProjectId, prom.Namespace, prom.Name)
			return
		}
	}
	ns = fmt.Sprintf("cattle-project-%s", projectId)
	return
}

func (r *PrometheusReconciler) CreateClusterPreset(presetBytes []byte) error {
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

func (r *PrometheusReconciler) CreateProjectPreset(p *monitoringv1.Prometheus, presetBytes []byte) error {
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

func (r *PrometheusReconciler) GeneratePresetForPrometheus(p monitoringv1.Prometheus, isDefault bool) mona.MonitoringPresets {
	var preset mona.MonitoringPresets

	preset.Spec.Monitoring.Agent = string(mona.AgentPrometheusOperator)
	svcmonLabels, ok := meta_util.LabelsForLabelSelector(p.Spec.ServiceMonitorSelector)
	if !ok {
		klog.Warningf("Prometheus %s/%s uses match expressions in ServiceMonitorSelector", p.Namespace, p.Name)
	}
	if len(svcmonLabels) == 0 {
		if isDefault && r.d.RancherManaged() {
			svcmonLabels = defaultRancherMonitoringLabels
		} else {
			svcmonLabels = defaultPrometheusStackLabels
		}
	}
	preset.Spec.Monitoring.ServiceMonitor.Labels = svcmonLabels

	preset.Form.Alert.Enabled = mona.SeverityFlagCritical
	ruleLabels, ok := meta_util.LabelsForLabelSelector(p.Spec.RuleSelector)
	if !ok {
		klog.Warningf("Prometheus %s/%s uses match expressions in RuleSelector", p.Namespace, p.Name)
	}
	if len(ruleLabels) == 0 {
		if isDefault && r.d.RancherManaged() {
			ruleLabels = defaultRancherMonitoringLabels
		} else {
			ruleLabels = defaultPrometheusStackLabels
		}
	}
	preset.Form.Alert.Labels = ruleLabels

	return preset
}

func (r *PrometheusReconciler) CreatePrometheusAppBinding(prom *monitoringv1.Prometheus, svc *mona.ServiceSpec) (kutil.VerbType, error) {
	ab := appcatalog.AppBinding{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      appBindingPrometheus,
			Namespace: prom.Namespace,
		},
	}

	sref, err := svc.ToServiceReference()
	if err != nil {
		return kutil.VerbUnchanged, err
	}

	var caBundle []byte
	// Try to get OpenShift CA bundle from kube-public/openshift-service-ca.crt
	if r.d.OpenShiftManaged() {
		caBundle, err = clustermeta.GetOpenShiftServiceSigner(r.kc)
		if err != nil {
			return kutil.VerbUnchanged, err
		}
	}

	vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &ab, func(in client.Object, createOp bool) client.Object {
		obj := in.(*appcatalog.AppBinding)

		ref := metav1.NewControllerRef(prom, schema.GroupVersionKind{
			Group:   monitoring.GroupName,
			Version: monitoringv1.Version,
			Kind:    "Prometheus",
		})
		obj.OwnerReferences = []metav1.OwnerReference{*ref}

		if obj.Annotations == nil {
			obj.Annotations = make(map[string]string)
		}
		obj.Annotations["monitoring.appscode.com/is-default-prometheus"] = "true"

		obj.Spec.Type = "Prometheus"
		obj.Spec.AppRef = &kmapi.TypedObjectReference{
			APIGroup:  monitoring.GroupName,
			Kind:      "Prometheus",
			Namespace: prom.Namespace,
			Name:      prom.Name,
		}
		obj.Spec.ClientConfig = appcatalog.ClientConfig{
			// URL:                   nil,
			Service: sref,
			// InsecureSkipTLSVerify: false,
			// CABundle:              nil,
			// ServerName:            "",
		}
		if r.d.OpenShiftManaged() {
			obj.Spec.ClientConfig.CABundle = caBundle
			obj.Spec.ClientConfig.ServerName = fmt.Sprintf("%s.%s.svc", svc.Name, svc.Namespace)
			obj.Spec.ClientConfig.InsecureSkipTLSVerify = false
		}

		return obj
	})
	if err == nil {
		klog.Infof("%s AppBinding %s/%s", vt, ab.Namespace, ab.Name)
	}
	return vt, err
}

func (r *PrometheusReconciler) CreateGrafanaAppBinding(prom *monitoringv1.Prometheus, resp *GrafanaDatasourceResponse) error {
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
			Version: monitoringv1.Version,
			Kind:    "Prometheus",
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
		obj.Spec.Secret = &appcatalog.TypedLocalObjectReference{
			APIGroup: "",
			Kind:     "Secret",
			Name:     ab.Name + "-auth",
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
func (r *PrometheusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	stateHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []reconcile.Request {
		var promList monitoringv1.PrometheusList
		err := r.kc.List(ctx, &promList)
		if err != nil {
			return nil
		}

		var req []reconcile.Request
		for _, prom := range promList.Items {
			req = append(req, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&prom)})
		}
		return req
	})

	bldr := ctrl.NewControllerManagedBy(mgr).
		For(&monitoringv1.Prometheus{}).
		Watches(&core.ConfigMap{}, stateHandler, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetNamespace() == metav1.NamespacePublic && obj.GetName() == kmapi.AceInfoConfigMapName
		})))
	if r.rancherAuthSecretName != "" {
		bldr = bldr.Watches(&core.Secret{}, stateHandler, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetNamespace() == selfNamespace && obj.GetName() == r.rancherAuthSecretName
		})))
	}
	return bldr.Complete(r)
}

func If[T any](condition bool, trueVal, falseVal T) T {
	if condition {
		return trueVal
	}
	return falseVal
}
