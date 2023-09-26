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

	"github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"gomodules.xyz/pointer"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	kutil "kmodules.xyz/client-go"
	kmapi "kmodules.xyz/client-go/api/v1"
	cu "kmodules.xyz/client-go/client"
	clustermeta "kmodules.xyz/client-go/cluster"
	core_util "kmodules.xyz/client-go/core/v1"
	meta_util "kmodules.xyz/client-go/meta"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	rsapi "kmodules.xyz/resource-metadata/apis/meta/v1alpha1"
	"kmodules.xyz/resource-metadata/apis/shared"
	"kmodules.xyz/resource-metadata/client/clientset/versioned"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	chartsapi "x-helm.dev/apimachinery/apis/charts/v1alpha1"
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

	cm := clustermeta.DetectClusterManager(r.kc)
	gvk := schema.GroupVersionKind{
		Group:   monitoring.GroupName,
		Version: monitoringv1.Version,
		Kind:    "Prometheus",
	}

	key := client.ObjectKeyFromObject(&prom)
	isDefault, err := r.IsDefault(cm, gvk, key)
	if err != nil {
		return ctrl.Result{}, err
	}

	if prom.DeletionTimestamp != nil {
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

		_, err = cu.CreateOrPatch(context.TODO(), r.kc, &prom, func(in client.Object, createOp bool) client.Object {
			obj := in.(*monitoringv1.Prometheus)
			prom.ObjectMeta = core_util.RemoveFinalizer(prom.ObjectMeta, mona.PrometheusKey)

			return obj
		})
		return ctrl.Result{}, err
	}

	_, err = cu.CreateOrPatch(context.TODO(), r.kc, &prom, func(in client.Object, createOp bool) client.Object {
		obj := in.(*monitoringv1.Prometheus)
		prom.ObjectMeta = core_util.AddFinalizer(prom.ObjectMeta, mona.PrometheusKey)

		return obj
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.SetupClusterForPrometheus(cm, &prom, isDefault); err != nil {
		log.Error(err, "unable to setup Prometheus")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *PrometheusReconciler) IsDefault(cm kmapi.ClusterManager, gvk schema.GroupVersionKind, key types.NamespacedName) (bool, error) {
	if cm.ManagedByRancher() {
		return key.Namespace == clustermeta.NamespaceRancherMonitoring &&
			key.Name == clustermeta.PrometheusRancherMonitoring, nil
	}
	return clustermeta.IsSingletonResource(r.kc, gvk, key)
}

func (r *PrometheusReconciler) findServiceForPrometheus(key types.NamespacedName) (*core.Service, error) {
	q := &rsapi.ResourceQuery{
		Request: &rsapi.ResourceQueryRequest{
			Source: rsapi.SourceInfo{
				Resource: kmapi.ResourceID{
					Group:   monitoring.GroupName,
					Version: monitoringv1.Version,
					Kind:    "Prometheus",
				},
				Namespace: key.Namespace,
				Name:      key.Name,
			},
			Target: &shared.ResourceLocator{
				Ref: metav1.GroupKind{
					Group: "",
					Kind:  "Service",
				},
				Query: shared.ResourceQuery{
					Type:    shared.GraphQLQuery,
					ByLabel: kmapi.EdgeLabelExposedBy,
				},
			},
			OutputFormat: rsapi.OutputFormatObject,
		},
		Response: nil,
	}
	var err error
	q, err = r.rmc.MetaV1alpha1().ResourceQueries().Create(context.TODO(), q, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	var list core.ServiceList
	err = json.Unmarshal(q.Response.Raw, &list)
	if err != nil {
		return nil, err
	}
	for _, svc := range list.Items {
		if svc.Spec.ClusterIP != "None" {
			return &svc, nil
		}
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{
		Group:    "",
		Resource: "services",
	}, key.String())
}

const (
	portPrometheus = "http-web"
	saTrickster    = "trickster"
)

func (r *PrometheusReconciler) SetupClusterForPrometheus(cm kmapi.ClusterManager, prom *monitoringv1.Prometheus, isDefault bool) error {
	key := client.ObjectKeyFromObject(prom)

	svc, err := r.findServiceForPrometheus(key)
	if err != nil {
		return err
	}

	// https://github.com/bytebuilders/installer/blob/master/charts/monitoring-config/templates/trickster/trickster.yaml
	sa := core.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saTrickster,
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

	role := rbac.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saTrickster,
			Namespace: key.Namespace,
		},
	}
	rolevt, err := cu.CreateOrPatch(context.TODO(), r.kc, &role, func(in client.Object, createOp bool) client.Object {
		obj := in.(*rbac.Role)
		ref := metav1.NewControllerRef(prom, schema.GroupVersionKind{
			Group:   monitoring.GroupName,
			Version: monitoringv1.Version,
			Kind:    "Prometheus",
		})
		obj.OwnerReferences = []metav1.OwnerReference{*ref}

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
	klog.Infof("%s role %s/%s", rolevt, role.Namespace, role.Name)

	rb := rbac.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saTrickster,
			Namespace: key.Namespace,
		},
	}
	rbvt, err := cu.CreateOrPatch(context.TODO(), r.kc, &rb, func(in client.Object, createOp bool) client.Object {
		obj := in.(*rbac.RoleBinding)
		ref := metav1.NewControllerRef(prom, schema.GroupVersionKind{
			Group:   rbac.GroupName,
			Version: "v1",
			Kind:    "Role",
		})
		obj.OwnerReferences = []metav1.OwnerReference{*ref}

		obj.RoleRef = rbac.RoleRef{
			APIGroup: rbac.GroupName,
			Kind:     "Role",
			Name:     role.Name,
		}

		obj.Subjects = []rbac.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      sa.Name,
				Namespace: sa.Namespace,
			},
		}

		return obj
	})
	if err != nil {
		return err
	}
	klog.Infof("%s role binding %s/%s", rbvt, rb.Namespace, rb.Name)

	err = r.CreatePreset(cm, prom, isDefault)
	if err != nil {
		return err
	}

	s, err := cu.GetServiceAccountTokenSecret(r.kc, client.ObjectKeyFromObject(&sa))
	if err != nil {
		return err
	}

	var pcfg mona.PrometheusConfig
	pcfg.Service = mona.ServiceSpec{
		Scheme:    "http",
		Name:      svc.Name,
		Namespace: svc.Namespace,
		Port:      "",
		Path:      "",
		Query:     "",
	}
	for _, p := range svc.Spec.Ports {
		if p.Name == portPrometheus {
			pcfg.Service.Port = fmt.Sprintf("%d", p.Port)
		}
	}
	pcfg.URL = fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s:%s:%s/proxy/", r.cfg.Host, pcfg.Service.Namespace, pcfg.Service.Scheme, pcfg.Service.Name, pcfg.Service.Port)
	// remove basic auth and client cert auth
	pcfg.BasicAuth = mona.BasicAuth{}
	pcfg.TLS.Cert = ""
	pcfg.TLS.Key = ""
	pcfg.BearerToken = string(s.Data["token"])
	pcfg.TLS.Ca = string(s.Data["ca.crt"])

	if r.bc != nil && sa.Annotations[registeredKey] != "true" {
		var projectId string
		if !isDefault {
			_, projectId, err = r.NamespaceForProjectSettings(prom)
			if err != nil {
				return err
			}
		}
		resp, err := r.bc.Register(mona.PrometheusContext{
			ClusterUID: r.clusterUID,
			ProjectId:  projectId,
			Default:    isDefault,
		}, pcfg)
		if err != nil {
			return err
		}

		if isDefault {
			_, err := r.CreatePrometheusAppBinding(prom, svc)
			if err != nil {
				return err
			}

			err = r.CreateGrafanaAppBinding(key, resp)
			if err != nil {
				return err
			}
		}

		savt, err = cu.CreateOrPatch(context.TODO(), r.kc, &sa, func(in client.Object, createOp bool) client.Object {
			obj := in.(*core.ServiceAccount)
			obj.Annotations = meta_util.OverwriteKeys(obj.Annotations, map[string]string{
				registeredKey: "true",
			})
			return obj
		})
		if err != nil {
			return err
		}
		klog.Infof("%s service account %s/%s with %s annotation", savt, sa.Namespace, sa.Name, registeredKey)
	}

	return nil
}

const (
	registeredKey        = mona.GroupName + "/registered"
	presetsMonitoring    = "monitoring-presets"
	appBindingPrometheus = "default-prometheus"
	appBindingGrafana    = "default-grafana"
)

var defaultPresetsLabels = map[string]string{
	"charts.x-helm.dev/is-default-preset": "true",
}

func (r *PrometheusReconciler) CreatePreset(cm kmapi.ClusterManager, p *monitoringv1.Prometheus, isDefault bool) error {
	presets := r.GeneratePresetForPrometheus(*p)
	presetBytes, err := json.Marshal(presets)
	if err != nil {
		return err
	}

	if cm.ManagedByRancher() {
		if isDefault {
			// create ClusterChartPreset
			err := r.CreateClusterPreset(presetBytes)
			if err != nil {
				return err
			}
		} else {
			// create ChartPreset
			// Decide NS
			err2 := r.CreateProjectPreset(p, presetBytes)
			if err2 != nil {
				return err2
			}
		}
		return nil
	}

	// create ClusterChartPreset
	return r.CreateClusterPreset(presetBytes)
}

func (r *PrometheusReconciler) NamespaceForProjectSettings(prom *monitoringv1.Prometheus) (ns string, projectId string, err error) {
	if prom.Namespace == clustermeta.NamespaceRancherMonitoring &&
		prom.Name == clustermeta.PrometheusRancherMonitoring {
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

func (r *PrometheusReconciler) GeneratePresetForPrometheus(p monitoringv1.Prometheus) mona.MonitoringPresets {
	var preset mona.MonitoringPresets

	preset.Spec.Monitoring.Agent = string(mona.AgentPrometheusOperator)
	svcmonLabels, ok := meta_util.LabelsForLabelSelector(p.Spec.ServiceMonitorSelector)
	if !ok {
		klog.Warningf("Prometheus %s/%s uses match expressions in ServiceMonitorSelector", p.Namespace, p.Name)
	}
	preset.Spec.Monitoring.ServiceMonitor.Labels = svcmonLabels

	preset.Form.Alert.Enabled = mona.SeverityFlagCritical
	ruleLabels, ok := meta_util.LabelsForLabelSelector(p.Spec.RuleSelector)
	if !ok {
		klog.Warningf("Prometheus %s/%s uses match expressions in RuleSelector", p.Namespace, p.Name)
	}
	preset.Form.Alert.Labels = ruleLabels

	return preset
}

func (r *PrometheusReconciler) CreatePrometheusAppBinding(p *monitoringv1.Prometheus, svc *core.Service) (kutil.VerbType, error) {
	ab := appcatalog.AppBinding{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      appBindingPrometheus,
			Namespace: p.Namespace,
		},
	}

	vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &ab, func(in client.Object, createOp bool) client.Object {
		obj := in.(*appcatalog.AppBinding)

		if obj.Annotations == nil {
			obj.Annotations = make(map[string]string)
		}
		obj.Annotations["monitoring.appscode.com/is-default-prometheus"] = "true"

		obj.Spec.Type = "Prometheus"
		obj.Spec.AppRef = &kmapi.TypedObjectReference{
			APIGroup:  monitoring.GroupName,
			Kind:      "Prometheus",
			Namespace: p.Namespace,
			Name:      p.Name,
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
			if p.Name == portPrometheus {
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

func (r *PrometheusReconciler) CreateGrafanaAppBinding(key types.NamespacedName, resp *GrafanaDatasourceResponse) error {
	ab := appcatalog.AppBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appBindingGrafana,
			Namespace: key.Namespace,
		},
	}

	abvt, err := cu.CreateOrPatch(context.TODO(), r.kc, &ab, func(in client.Object, createOp bool) client.Object {
		obj := in.(*appcatalog.AppBinding)

		if obj.Annotations == nil {
			obj.Annotations = make(map[string]string)
		}
		obj.Annotations["monitoring.appscode.com/is-default-grafana"] = "true"

		obj.Spec.Type = "Grafana"
		obj.Spec.AppRef = nil
		obj.Spec.ClientConfig = appcatalog.ClientConfig{
			URL: pointer.StringP(resp.Grafana.URL),
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
				Namespace: key.Namespace,
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&monitoringv1.Prometheus{}).
		Complete(r)
}
