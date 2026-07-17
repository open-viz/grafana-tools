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

	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/pkg/controllers/prometheus"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	cu "kmodules.xyz/client-go/client"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// appBindingExists reports whether the named AppBinding is present in the given namespace.
func (r *ClientOrgReconciler) appBindingExists(ctx context.Context, namespace, name string) bool {
	var ab appcatalog.AppBinding
	return r.kc.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &ab) == nil
}

// setNamespaceMarker stamps the shared registration marker on the namespace.
func (r *ClientOrgReconciler) setNamespaceMarker(ctx context.Context, monNamespace *core.Namespace, state string) error {
	rbvt, err := cu.CreateOrPatch(ctx, r.kc, monNamespace, func(in client.Object, _ bool) client.Object {
		obj := in.(*core.Namespace)
		if obj.Annotations == nil {
			obj.Annotations = map[string]string{}
		}
		obj.Annotations[prometheus.RegisteredKey] = state
		return obj
	})
	if err != nil {
		return err
	}
	klog.Infof("%s namespace %s with %s annotation", rbvt, monNamespace.Name, prometheus.RegisteredKey)
	return nil
}

// registerGrafanaBackend registers the client-org against the Grafana backend and creates its
// AppBinding. It does not stamp the marker; the caller stamps once both backends succeed.
func (r *ClientOrgReconciler) registerGrafanaBackend(monNamespace string, pcfg mona.PrometheusConfig, clientOrgId string) error {
	resp, err := r.bc.Register(mona.PrometheusContext{
		HubUID:      r.hubUID,
		ClusterUID:  r.clusterUID,
		ProjectId:   "",
		Default:     false,
		IssueToken:  true,
		ClientOrgID: clientOrgId,
	}, pcfg)
	if err != nil {
		return err
	}
	return r.CreateGrafanaAppBinding(monNamespace, resp)
}

// registerPersesBackend registers the client-org against the Perses backend and creates its
// AppBinding. It does not stamp the marker; the caller stamps once both backends succeed.
func (r *ClientOrgReconciler) registerPersesBackend(monNamespace string, pcfg mona.PrometheusConfig, clientOrgId string) error {
	persesResp, err := r.bc.RegisterPerses(mona.PrometheusContext{
		HubUID:      r.hubUID,
		ClusterUID:  r.clusterUID,
		ProjectId:   "",
		Default:     false,
		IssueToken:  true,
		ClientOrgID: clientOrgId,
	}, pcfg)
	if err != nil {
		return err
	}
	return r.CreatePersesAppBinding(monNamespace, persesResp)
}

func (r *ClientOrgReconciler) CreateGrafanaAppBinding(monNamespace string, resp *prometheus.GrafanaDatasourceResponse) error {
	ab := appcatalog.AppBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      abClientOrgGrafana,
			Namespace: monNamespace,
		},
	}

	abvt, err := cu.CreateOrPatch(context.TODO(), r.kc, &ab, func(in client.Object, createOp bool) client.Object {
		obj := in.(*appcatalog.AppBinding)

		//ref := metav1.NewControllerRef(prom, schema.GroupVersionKind{
		//	Group:   monitoring.GroupName,
		//	Version: monitoringv1.Version,
		//	Kind:    "Prometheus",
		//})
		//obj.OwnerReferences = []metav1.OwnerReference{*ref}
		//
		//if obj.Annotations == nil {
		//	obj.Annotations = make(map[string]string)
		//}
		//obj.Annotations["monitoring.appscode.com/is-default-grafana"] = "true"

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
				Namespace: monNamespace,
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

func (r *ClientOrgReconciler) CreatePersesAppBinding(monNamespace string, resp *prometheus.PersesDatasourceResponse) error {
	ab := appcatalog.AppBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      abClientOrgPerses,
			Namespace: monNamespace,
		},
	}

	abvt, err := cu.CreateOrPatch(context.TODO(), r.kc, &ab, func(in client.Object, createOp bool) client.Object {
		obj := in.(*appcatalog.AppBinding)

		//ref := metav1.NewControllerRef(prom, schema.GroupVersionKind{
		//	Group:   monitoring.GroupName,
		//	Version: monitoringv1.Version,
		//	Kind:    "Prometheus",
		//})
		//obj.OwnerReferences = []metav1.OwnerReference{*ref}
		//
		//if obj.Annotations == nil {
		//	obj.Annotations = make(map[string]string)
		//}
		//obj.Annotations["monitoring.appscode.com/is-default-grafana"] = "true"

		obj.Spec.Type = "Perses"
		obj.Spec.AppRef = nil
		obj.Spec.ClientConfig = appcatalog.ClientConfig{
			URL: ptr.To(resp.Perses.URL),
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
			Name:     ab.Name + "-auth",
			APIGroup: "",
			Kind:     "Secret",
		}

		// TODO: handle TLS config returned in resp
		if caCert := r.bc.CACert(); len(caCert) > 0 {
			obj.Spec.ClientConfig.CABundle = caCert
		}

		params := openvizapi.PersesConfiguration{
			TypeMeta: metav1.TypeMeta{
				Kind:       "PersesConfiguration",
				APIVersion: openvizapi.SchemeGroupVersion.String(),
			},
			Datasource:  resp.Datasource,
			FolderName:  resp.FolderName,
			ProjectName: resp.ProjectName,
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
				Namespace: monNamespace,
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
				"token": resp.Perses.BearerToken,
			}

			return obj
		})
		if e2 == nil {
			klog.Infof("%s Grafana auth secret %s/%s", svt, authSecret.Namespace, authSecret.Name)
		}
	}

	return err
}
