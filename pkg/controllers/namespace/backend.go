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
	"fmt"

	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/pkg/controllers/prometheus"

	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	kutil "kmodules.xyz/client-go"
	cu "kmodules.xyz/client-go/client"
	clustermeta "kmodules.xyz/client-go/cluster"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ClientOrgReconciler) appBindingExists(ctx context.Context, namespace, name string) bool {
	var ab appcatalog.AppBinding
	return r.kc.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &ab) == nil
}

func (r *ClientOrgReconciler) setNamespaceMarker(ctx context.Context, monNamespace *core.Namespace, state string) error {
	rbvt, err := cu.Patch(ctx, r.kc, monNamespace, func(in client.Object) client.Object {
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
	if rbvt != kutil.VerbUnchanged {
		klog.Infof("%s annotation %s on Namespace %s", rbvt, prometheus.RegisteredKey, monNamespace.Name)
	}
	return nil
}

func (r *ClientOrgReconciler) buildPrometheusConfig(ctx context.Context, promKey client.ObjectKey, svcProm core.Service) (mona.PrometheusConfig, error) {
	var rbProm rbac.RoleBinding
	if err := r.kc.Get(ctx, client.ObjectKey{Name: prometheus.CRTrickster, Namespace: svcProm.Namespace}, &rbProm); err != nil {
		return mona.PrometheusConfig{}, err
	}
	if rbProm.Annotations[prometheus.RegisteredKey] == "" {
		return mona.PrometheusConfig{}, fmt.Errorf("rolebinding %s/%s is not registered yet", rbProm.Namespace, rbProm.Name)
	}

	var pcfg mona.PrometheusConfig
	pcfg.Service = r.d.Service(promKey, &svcProm)

	// remove basic auth and client cert auth
	pcfg.BearerToken = "" // set in b3, except for OpenShift
	pcfg.BasicAuth = mona.BasicAuth{}
	pcfg.TLS.Cert = ""
	pcfg.TLS.Key = ""
	pcfg.TLS.Ca = "" // set in b3

	if r.d.OpenShiftManaged() {
		klog.V(4).Infof("building OpenShift route-based Prometheus config for %s/%s", svcProm.Namespace, svcProm.Name)
		domain, err := clustermeta.GetOpenShiftAppsDomain(r.kc)
		if err != nil {
			return mona.PrometheusConfig{}, err
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
			return mona.PrometheusConfig{}, err
		}
		pcfg.BearerToken = string(s.Data["token"])
	}

	return pcfg, nil
}

// registerBackends re-registers each backend when the shared marker is stale (cluster state
// changed) or its AppBinding is missing. Errors are aggregated so a hub failure on one backend
// does not block the other.
func (r *ClientOrgReconciler) registerBackends(ctx context.Context, monNamespace *core.Namespace, pcfg mona.PrometheusConfig, clientOrgId, state string) error {
	var errs []error

	grafanaOK, persesOK := true, true

	if monNamespace.Annotations[prometheus.RegisteredKey] != state ||
		!r.appBindingExists(ctx, monNamespace.Name, abClientOrgGrafana) {
		klog.V(4).Infof("registering grafana backend for client-org %s (marker=%q, want=%q)", clientOrgId, monNamespace.Annotations[prometheus.RegisteredKey], state)
		if err := r.registerGrafanaBackend(monNamespace.Name, pcfg, clientOrgId); err != nil {
			errs = append(errs, err)
			grafanaOK = false
		}
	} else {
		klog.V(4).Infof("grafana backend already registered for client-org %s, skipping", clientOrgId)
	}

	if monNamespace.Annotations[prometheus.RegisteredKey] != state ||
		!r.appBindingExists(ctx, monNamespace.Name, abClientOrgPerses) {
		klog.V(4).Infof("registering perses backend for client-org %s (marker=%q, want=%q)", clientOrgId, monNamespace.Annotations[prometheus.RegisteredKey], state)
		if err := r.registerPersesBackend(monNamespace.Name, pcfg, clientOrgId); err != nil {
			errs = append(errs, err)
			persesOK = false
		}
	} else {
		klog.V(4).Infof("perses backend already registered for client-org %s, skipping", clientOrgId)
	}

	// Stamp the marker only after BOTH backends succeed, otherwise a failed backend's block is
	// skipped on the next reconcile and its AppBinding is never recreated.
	if grafanaOK && persesOK {
		klog.V(4).Infof("both backends registered for client-org %s, stamping marker %s=%s", clientOrgId, prometheus.RegisteredKey, state)
		if err := r.setNamespaceMarker(ctx, monNamespace, state); err != nil {
			errs = append(errs, err)
		}
	} else {
		klog.V(4).Infof("skipping marker stamp for client-org %s (grafanaOK=%t, persesOK=%t)", clientOrgId, grafanaOK, persesOK)
	}

	return utilerrors.NewAggregate(errs)
}

func (r *ClientOrgReconciler) registerGrafanaBackend(monNamespace string, pcfg mona.PrometheusConfig, clientOrgId string) error {
	resp, err := r.pc.Register(mona.PrometheusContext{
		HubUID:      r.hubUID,
		ClusterUID:  r.clusterUID,
		ProjectId:   "",
		Default:     false,
		IssueToken:  true,
		ClientOrgID: clientOrgId,
	}, pcfg)
	if err != nil {
		return fmt.Errorf("failed to register grafana backend for client-org %s: %w", clientOrgId, err)
	}
	klog.Infof("registered grafana backend for client-org %s in namespace %s", clientOrgId, monNamespace)
	return r.CreateGrafanaAppBinding(monNamespace, resp)
}

func (r *ClientOrgReconciler) registerPersesBackend(monNamespace string, pcfg mona.PrometheusConfig, clientOrgId string) error {
	persesResp, err := r.pc.RegisterPerses(mona.PrometheusContext{
		HubUID:      r.hubUID,
		ClusterUID:  r.clusterUID,
		ProjectId:   "",
		Default:     false,
		IssueToken:  true,
		ClientOrgID: clientOrgId,
	}, pcfg)
	if err != nil {
		return fmt.Errorf("failed to register perses backend for client-org %s: %w", clientOrgId, err)
	}
	klog.Infof("registered perses backend for client-org %s in namespace %s", clientOrgId, monNamespace)
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

		obj.Spec.Type = "Grafana"
		obj.Spec.AppRef = nil
		obj.Spec.ClientConfig = appcatalog.ClientConfig{
			URL: ptr.To(resp.Grafana.URL),
		}
		obj.Spec.Secret = &appcatalog.TypedLocalObjectReference{
			APIGroup: "",
			Kind:     "Secret",
			Name:     ab.Name + "-auth",
		}

		// TODO: handle TLS config returned in resp
		if caCert := r.pc.CACert(); len(caCert) > 0 {
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

		obj.Spec.Type = "Perses"
		obj.Spec.AppRef = nil
		obj.Spec.ClientConfig = appcatalog.ClientConfig{
			URL: ptr.To(resp.Perses.URL),
		}
		obj.Spec.Secret = &appcatalog.TypedLocalObjectReference{
			Name:     ab.Name + "-auth",
			APIGroup: "",
			Kind:     "Secret",
		}

		// TODO: handle TLS config returned in resp
		if caCert := r.pc.CACert(); len(caCert) > 0 {
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
