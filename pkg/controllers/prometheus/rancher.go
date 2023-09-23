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
	"sort"

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
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	kutil "kmodules.xyz/client-go"
	kmapi "kmodules.xyz/client-go/api/v1"
	cu "kmodules.xyz/client-go/client"
	clustermeta "kmodules.xyz/client-go/cluster"
	meta_util "kmodules.xyz/client-go/meta"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	rsapi "kmodules.xyz/resource-metadata/apis/meta/v1alpha1"
	"kmodules.xyz/resource-metadata/apis/shared"
	"sigs.k8s.io/controller-runtime/pkg/client"
	chartsapi "x-helm.dev/apimachinery/apis/charts/v1alpha1"
)

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

func (r *PrometheusReconciler) SetupClusterForPrometheus(key types.NamespacedName) error {
	cm := clustermeta.DetectClusterManager(r.kc)

	gvk := schema.GroupVersionKind{
		Group:   monitoring.GroupName,
		Version: monitoringv1.Version,
		Kind:    "Prometheus",
	}

	var prom monitoringv1.Prometheus
	err := r.kc.Get(context.TODO(), key, &prom)
	if err != nil {
		return err
	}

	key = client.ObjectKeyFromObject(&prom)
	isDefault, err := clustermeta.IsDefault(r.kc, cm, gvk, key)
	if err != nil {
		return err
	}

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
		ref := metav1.NewControllerRef(&prom, schema.GroupVersionKind{
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
		ref := metav1.NewControllerRef(&prom, schema.GroupVersionKind{
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
		ref := metav1.NewControllerRef(&prom, schema.GroupVersionKind{
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

	err = r.CreatePreset(cm, &prom, isDefault)
	if err != nil {
		return err
	}

	var caData, tokenData []byte
	err = wait.PollImmediate(kutil.RetryInterval, kutil.ReadinessTimeout, func() (done bool, err error) {
		var sacc core.ServiceAccount
		err = r.kc.Get(context.TODO(), client.ObjectKeyFromObject(&sa), &sacc)
		if apierrors.IsNotFound(err) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		if len(sacc.Secrets) == 0 {
			return false, nil
		}

		skey := client.ObjectKey{
			Namespace: sa.Namespace,
			Name:      sacc.Secrets[0].Name,
		}
		var s core.Secret
		err = r.kc.Get(context.TODO(), skey, &s)
		if apierrors.IsNotFound(err) {
			return false, nil
		} else if err != nil {
			return false, err
		}

		var caFound, tokenFound bool
		caData, caFound = s.Data["ca.crt"]
		tokenData, tokenFound = s.Data["token"]
		return caFound && tokenFound, nil
	})
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
	pcfg.BearerToken = string(tokenData)
	pcfg.TLS.Ca = string(caData)

	if isDefault {
		// create Prometheus AppBinding
		vt, err := r.CreatePrometheusAppBinding(&prom, svc)
		if err != nil {
			return err
		}
		if vt == kutil.VerbCreated {
			resp, err := r.bc.Register("", pcfg)
			if err != nil {
				return err
			}

			// create Grafana AppBinding
			return r.CreateGrafanaAppBinding(key, resp)
		}
	}

	return nil
}

const presetsMonitoring = "monitoring-presets"

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

func (r *PrometheusReconciler) NamespaceForPreset(prom *monitoringv1.Prometheus) (string, error) {
	ls := prom.Spec.ServiceMonitorNamespaceSelector
	if ls.MatchLabels == nil {
		ls.MatchLabels = make(map[string]string)
	}
	ls.MatchLabels[clustermeta.LabelKeyRancherHelmProjectOperated] = "true"
	sel, err := metav1.LabelSelectorAsSelector(ls)
	if err != nil {
		return "", err
	}

	var nsList core.NamespaceList
	err = r.kc.List(context.TODO(), &nsList, client.MatchingLabelsSelector{Selector: sel})
	if err != nil {
		return "", err
	}
	namespaces := nsList.Items
	if len(namespaces) == 0 {
		return "", fmt.Errorf("failed to select AppBinding namespace for Prometheus %s/%s", prom.Namespace, prom.Name)
	}
	sort.Slice(namespaces, func(i, j int) bool {
		return namespaces[i].CreationTimestamp.Before(&namespaces[j].CreationTimestamp)
	})
	return namespaces[0].Name, nil
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
	ns, err := r.NamespaceForPreset(p)
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
		klog.Warningln("Prometheus %s/%s uses match expressions in ServiceMonitorSelector", p.Namespace, p.Name)
	}
	preset.Spec.Monitoring.ServiceMonitor.Labels = svcmonLabels

	preset.Form.Alert.Enabled = mona.SeverityFlagCritical
	ruleLabels, ok := meta_util.LabelsForLabelSelector(p.Spec.RuleSelector)
	if !ok {
		klog.Warningln("Prometheus %s/%s uses match expressions in RuleSelector", p.Namespace, p.Name)
	}
	preset.Form.Alert.Labels = ruleLabels

	return preset
}

func (r *PrometheusReconciler) CreatePrometheusAppBinding(p *monitoringv1.Prometheus, svc *core.Service) (kutil.VerbType, error) {
	ab := appcatalog.AppBinding{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-prometheus",
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

func (r *PrometheusReconciler) CreateGrafanaAppBinding(key types.NamespacedName, config *GrafanaDatasourceResponse) error {
	ab := appcatalog.AppBinding{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-grafana",
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
			URL: pointer.StringP(config.Grafana.URL),
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
			TypeMeta:   metav1.TypeMeta{},
			Datasource: config.Datasource,
			FolderID:   config.FolderID,
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
				"token": config.Grafana.BearerToken,
			}

			return obj
		})
		if e2 == nil {
			klog.Infof("%s Grafana auth secret %s/%s", svt, authSecret.Namespace, authSecret.Name)
		}
	}

	return err
}
