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
	"bytes"
	"context"
	"fmt"

	"go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/pkg/controllers/clientorg"
	"go.openviz.dev/grafana-tools/pkg/controllers/prometheus"
	"go.openviz.dev/grafana-tools/pkg/detector"

	"github.com/grafana-tools/sdk"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	"kmodules.xyz/client-go/meta"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ClientOrgReconciler reconciles a GatewayConfig object
type ClientOrgReconciler struct {
	kc         client.Client
	scheme     *runtime.Scheme
	bc         *prometheus.Client
	clusterUID string
	hubUID     string
	d          detector.PrometheusDetector
}

func NewReconciler(kc client.Client, bc *prometheus.Client, clusterUID, hubUID string, d detector.PrometheusDetector) *ClientOrgReconciler {
	return &ClientOrgReconciler{
		kc:         kc,
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

	crClientOrgMonitoring = "appscode:client-org:monitoring"
	abClientOrgGrafana    = "grafana"
)

func (r *ClientOrgReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

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
		return ctrl.Result{}, fmt.Errorf("client organization mode is not supported when federated prometheus is used in a Rancher managed cluster")
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
	if err := r.kc.Get(ctx, client.ObjectKeyFromObject(&monNamespace), &monNamespace); apierrors.IsNotFound(err) {
		if err := r.kc.Create(ctx, &monNamespace); err != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
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

	// confirm trickter sa registered
	var saList core.ServiceAccountList
	if err := r.kc.List(ctx, &saList); err != nil {
		return ctrl.Result{}, err
	}
	var saKey types.NamespacedName
	for _, sa := range saList.Items {
		if sa.Name != prometheus.ServiceAccountTrickster {
			continue
		}
		if sa.Annotations[prometheus.RegisteredKey] != "" {
		}

		saKey = types.NamespacedName{
			Namespace: sa.Namespace,
			Name:      sa.Name,
		}
		break
	}
	if saKey.Namespace == "" {
		return ctrl.Result{}, fmt.Errorf("service account %s is not registered", prometheus.ServiceAccountTrickster)
	}

	var promList monitoringv1.PrometheusList
	if err := r.kc.List(ctx, &promList, client.InNamespace(saKey.Namespace)); err != nil {
		return ctrl.Result{}, err
	} else if len(promList.Items) == 0 {
		return ctrl.Result{}, fmt.Errorf("prometheus not found in namespace %s", saKey.Namespace)
	} else if len(promList.Items) > 1 {
		return ctrl.Result{}, fmt.Errorf("more than one prometheus found in namespace %s", saKey.Namespace)
	}

	var promService core.Service
	err = r.kc.Get(context.TODO(), client.ObjectKeyFromObject(promList.Items[0]), &promService)
	if err != nil {
		return ctrl.Result{}, err
	}

	var saToken string
	var caCrt string
	s, err := cu.GetServiceAccountTokenSecret(r.kc, saKey)
	if err != nil {
		return ctrl.Result{}, err
	}
	saToken = string(s.Data["token"])
	caCrt = string(s.Data["ca.crt"])

	var pcfg mona.PrometheusConfig
	pcfg.Service = mona.ServiceSpec{
		Scheme:    "http",
		Name:      promService.Name,
		Namespace: promService.Namespace,
		Port:      "",
		Path:      "",
		Query:     "",
	}
	for _, p := range promService.Spec.Ports {
		if p.Name == prometheus.PortPrometheus {
			pcfg.Service.Port = fmt.Sprintf("%d", p.Port)
		}
	}
	// pcfg.URL = fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s:%s:%s/proxy/", r.cfg.Host, pcfg.Service.Namespace, pcfg.Service.Scheme, pcfg.Service.Name, pcfg.Service.Port)

	// remove basic auth and client cert auth
	//if rancherToken != nil {
	//	pcfg.BearerToken = rancherToken.Token
	//} else {
	pcfg.BearerToken = saToken
	// }
	pcfg.BasicAuth = mona.BasicAuth{}
	pcfg.TLS.Cert = ""
	pcfg.TLS.Key = ""
	pcfg.TLS.Ca = caCrt

	cm, err := clustermeta.ClusterMetadata(r.kc)
	if err != nil {
		return ctrl.Result{}, err
	}
	state := cm.State()

	applyMarkers := func(in client.Object, createOp bool) client.Object {
		obj := in.(*core.Namespace)
		if obj.Annotations == nil {
			obj.Annotations = map[string]string{}
		}
		obj.Annotations[prometheus.RegisteredKey] = state
		return obj
	}

	if r.bc != nil &&
		monNamespace.Annotations[prometheus.RegisteredKey] != state {
		resp, err := r.bc.Register(mona.PrometheusContext{
			HubUID:      r.hubUID,
			ClusterUID:  r.clusterUID,
			ProjectId:   "",
			Default:     false,
			IssueToken:  true,
			ClientOrgID: clientOrgId,
		}, pcfg)
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.CreateGrafanaAppBinding(monNamespace.Name, resp)
		if err != nil {
			return ctrl.Result{}, err
		}

		rbvt, err := cu.CreateOrPatch(context.TODO(), r.kc, &monNamespace, applyMarkers)
		if err != nil {
			return ctrl.Result{}, err
		}
		klog.Infof("%s namespace %s with %s annotation", rbvt, monNamespace.Name, prometheus.RegisteredKey)
	}

	var dashboardList v1alpha1.GrafanaDashboardList
	if err := r.kc.List(ctx, &dashboardList); err != nil {
		return ctrl.Result{}, err
	}
	for _, dashboard := range dashboardList.Items {
		if dashboard.Spec.Model == nil {
			return ctrl.Result{}, apierrors.NewBadRequest(fmt.Sprintf("GrafanaDashboard %s/%s is missing a model", dashboard.Namespace, dashboard.Name))
		}
		var board sdk.Board
		err = json.Unmarshal(dashboard.Spec.Model.Raw, &board)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to unmarshal model for GrafanaDashboard %s/%s, reason: %v", dashboard.Namespace, dashboard.Name, err)
		}
		for idx, item := range board.Templating.List {
			if item.Name != "namespace" {
				continue
			}

			item.Type = "constant"
			item.Query = ns.Name
			board.Templating.List[idx] = item
		}
		board.Title = detector.ClientDashboardTitle(board.Title)
		boardBytes, err := json.Marshal(board)
		if err != nil {
			return ctrl.Result{}, err
		}

		if _, found := dashboard.Annotations[srcRefKey]; found && bytes.Equal(dashboard.Spec.Model.Raw, boardBytes) {
			continue
		}

		copiedDashboard := &v1alpha1.GrafanaDashboard{}
		err = r.kc.Get(context.TODO(), types.NamespacedName{
			Namespace: monNamespace.Name,
			Name:      dashboard.Name,
		}, copiedDashboard)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			copiedDashboard = dashboard.DeepCopy()
			copiedDashboard.Namespace = monNamespace.Name
		} else {
			copiedDashboard.Spec = *dashboard.Spec.DeepCopy()
		}

		klog.Infof("------------------------- original= %v/%v , copy-to-ns= %v \n", dashboard.Namespace, dashboard.Name, copiedDashboard.Namespace)

		opresult, err := cu.CreateOrPatch(ctx, r.kc, copiedDashboard, func(obj client.Object, createOp bool) client.Object {
			if createOp {
				copiedDashboard.ResourceVersion = ""
			}

			if copiedDashboard.Annotations == nil {
				copiedDashboard.Annotations = map[string]string{}
			}
			copiedDashboard.Annotations[srcRefKey] = client.ObjectKeyFromObject(&dashboard).String()
			copiedDashboard.Annotations[srcHashKey] = meta.ObjectHash(&dashboard)

			// use client Grafana appbinding
			copiedDashboard.Spec.GrafanaRef = &kmapi.ObjectReference{
				Namespace: monNamespace.Name,
				Name:      abClientOrgGrafana,
			}
			copiedDashboard.Spec.Model = &runtime.RawExtension{
				Raw: boardBytes,
			}

			return copiedDashboard
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		if opresult != kutil.VerbUnchanged {
			log.Info(fmt.Sprintf("%s GrafanaDashboard %s/%s", opresult, copiedDashboard.Namespace, copiedDashboard.Name))
		}
	}

	return ctrl.Result{}, nil
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

// SetupWithManager sets up the controller with the Manager.
func (r *ClientOrgReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&core.Namespace{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetLabels()[kmapi.ClientOrgKey] == "true"
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
