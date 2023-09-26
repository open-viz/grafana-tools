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
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// FederationReconciler reconciles a ServiceMonitor object
type FederationReconciler struct {
	cfg    *rest.Config
	kc     client.Client
	scheme *runtime.Scheme
}

func NewFederationReconciler(cfg *rest.Config, kc client.Client) *FederationReconciler {
	return &FederationReconciler{
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
func (r *FederationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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
	if !found || val != mona.PrometheusValueFederated {
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

	// services
	srcServices := map[client.ObjectKey]core.Service{}
	svcSel, err := metav1.LabelSelectorAsSelector(&svcMon.Spec.Selector)
	if err != nil {
		return ctrl.Result{}, err
	}
	for _, ns := range svcMon.Spec.NamespaceSelector.MatchNames {
		var svcList core.ServiceList
		err = r.kc.List(context.TODO(), &svcList, client.InNamespace(ns), client.MatchingLabelsSelector{
			Selector: svcSel,
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		for _, svc := range svcList.Items {
			srcServices[client.ObjectKeyFromObject(&svc)] = svc
		}
	}

	// secret
	var srcSecrets []core.Secret
	for i := range svcMon.Spec.Endpoints {
		e := svcMon.Spec.Endpoints[i]

		if e.TLSConfig != nil &&
			e.TLSConfig.CA.Secret != nil &&
			e.TLSConfig.CA.Secret.Name != "" {

			key := client.ObjectKey{
				Name:      e.TLSConfig.CA.Secret.Name,
				Namespace: svcMon.Namespace,
			}

			var srcSecret core.Secret
			err := r.kc.Get(context.TODO(), key, &srcSecret)
			if err != nil {
				return ctrl.Result{}, err
			}
			srcSecrets = append(srcSecrets, srcSecret)
		}
	}

	var errList []error
	for _, prom := range promList.Items {
		isDefault := IsDefaultPrometheus(prom)

		if !isDefault && prom.Namespace == req.Namespace {
			err := fmt.Errorf("federated service monitor can't be in the same namespace with project Prometheus %s/%s", prom.Namespace, prom.Namespace)
			log.Error(err, "bad service monitor")
			return ctrl.Result{}, nil // don't retry until svcmon changes
		}

		if isDefault {
			if err := r.updateServiceMonitorLabels(prom, &svcMon); err != nil {
				errList = append(errList, err)
			}
		} else {
			targetSvcMon, err := r.copyServiceMonitor(prom, &svcMon)
			if err != nil {
				errList = append(errList, err)
			}

			for _, srcSvc := range srcServices {
				if err := r.copyService(&srcSvc, targetSvcMon); err != nil {
					errList = append(errList, err)
				}
				var srcEP core.Endpoints
				err = r.kc.Get(context.TODO(), client.ObjectKeyFromObject(&srcSvc), &srcEP)
				if err != nil {
					errList = append(errList, err)
				} else {
					if err := r.copyEndpoints(&srcSvc, &srcEP, targetSvcMon); err != nil {
						errList = append(errList, err)
					}
				}
			}

			for _, srcSecret := range srcSecrets {
				if err := r.copySecret(&srcSecret, targetSvcMon); err != nil {
					errList = append(errList, err)
				}
			}
		}
	}

	return ctrl.Result{}, errors.NewAggregate(errList)
}

func IsDefaultPrometheus(prom *monitoringv1.Prometheus) bool {
	expected := client.ObjectKey{
		Namespace: clustermeta.NamespaceRancherMonitoring,
		Name:      clustermeta.PrometheusRancherMonitoring,
	}
	pk := client.ObjectKeyFromObject(prom)
	return pk == expected
}

func (r *FederationReconciler) updateServiceMonitorLabels(prom *monitoringv1.Prometheus, src *monitoringv1.ServiceMonitor) error {
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

func (r *FederationReconciler) copyServiceMonitor(prom *monitoringv1.Prometheus, src *monitoringv1.ServiceMonitor) (*monitoringv1.ServiceMonitor, error) {
	sel, err := metav1.LabelSelectorAsSelector(prom.Spec.ServiceMonitorNamespaceSelector)
	if err != nil {
		return nil, err
	}

	var nsList core.NamespaceList
	err = r.kc.List(context.TODO(), &nsList, client.MatchingLabelsSelector{
		Selector: sel,
	})
	if err != nil {
		return nil, err
	}

	namespaces := make([]string, 0, len(nsList.Items))
	namespaces = append(namespaces, "") // for non-namespaced resources
	for _, ns := range nsList.Items {
		if ns.Name == fmt.Sprintf("cattle-project-%s", ns.Labels[clustermeta.LabelKeyRancherFieldProjectId]) {
			continue
		}
		namespaces = append(namespaces, ns.Name)
	}
	sort.Strings(namespaces)

	target := monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      src.Name,
			Namespace: prom.Namespace,
		},
	}
	vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &target, func(in client.Object, createOp bool) client.Object {
		obj := in.(*monitoringv1.ServiceMonitor)

		ref := metav1.NewControllerRef(prom, schema.GroupVersionKind{
			Group:   monitoring.GroupName,
			Version: monitoringv1.Version,
			Kind:    "Prometheus",
		})
		obj.OwnerReferences = []metav1.OwnerReference{*ref}

		labels, _ := meta_util.LabelsForLabelSelector(prom.Spec.ServiceMonitorSelector)
		obj.Labels = meta_util.OverwriteKeys(obj.Labels, labels)
		delete(obj.Labels, mona.PrometheusKey)

		obj.Spec = *src.Spec.DeepCopy()

		keepNSMetrics := monitoringv1.RelabelConfig{
			Action:       "keep",
			SourceLabels: []monitoringv1.LabelName{"namespace"},
			Regex:        "(" + strings.Join(namespaces, "|") + ")",
		}
		for i := range obj.Spec.Endpoints {
			e := obj.Spec.Endpoints[i]

			e.HonorLabels = true // keep original labels

			if len(e.MetricRelabelConfigs) == 0 || !reflect.DeepEqual(keepNSMetrics, *e.MetricRelabelConfigs[0]) {
				e.MetricRelabelConfigs = append([]*monitoringv1.RelabelConfig{
					&keepNSMetrics,
				}, e.MetricRelabelConfigs...)
			}
			obj.Spec.Endpoints[i] = e
		}

		return obj
	})
	if err != nil {
		return nil, err
	}
	klog.Infof("%s ServiceMonitor %s/%s", vt, target.Namespace, target.Name)
	return &target, nil
}

func (r *FederationReconciler) copySecret(src *core.Secret, targetSvcMon *monitoringv1.ServiceMonitor) error {
	target := core.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      src.Name,
			Namespace: targetSvcMon.Namespace,
		},
		Immutable:  nil,
		Data:       nil,
		StringData: nil,
		Type:       "",
	}

	vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &target, func(in client.Object, createOp bool) client.Object {
		obj := in.(*core.Secret)

		ref := metav1.NewControllerRef(targetSvcMon, schema.GroupVersionKind{
			Group:   monitoring.GroupName,
			Version: monitoringv1.Version,
			Kind:    "ServiceMonitor",
		})
		obj.OwnerReferences = []metav1.OwnerReference{*ref}

		obj.Immutable = src.Immutable
		obj.Data = src.Data
		obj.Type = src.Type

		return obj
	})
	if err != nil {
		return err
	}
	klog.Infof("%s Secret %s/%s", vt, target.Namespace, target.Name)
	return nil
}

func (r *FederationReconciler) copyService(src *core.Service, targetSvcMon *monitoringv1.ServiceMonitor) error {
	target := core.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      src.Name,
			Namespace: targetSvcMon.Namespace,
		},
	}

	vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &target, func(in client.Object, createOp bool) client.Object {
		obj := in.(*core.Service)

		ref := metav1.NewControllerRef(targetSvcMon, schema.GroupVersionKind{
			Group:   monitoring.GroupName,
			Version: monitoringv1.Version,
			Kind:    "ServiceMonitor",
		})
		obj.OwnerReferences = []metav1.OwnerReference{*ref}
		obj.Labels = meta_util.OverwriteKeys(obj.Labels, src.Labels)
		obj.Annotations = meta_util.OverwriteKeys(obj.Annotations, src.Annotations)

		obj.Spec.Type = core.ServiceTypeClusterIP

		for _, port := range src.Spec.Ports {
			obj.Spec.Ports = UpsertServicePort(obj.Spec.Ports, port)
		}
		return obj
	})
	if err != nil {
		return err
	}
	klog.Infof("%s Service %s/%s", vt, target.Namespace, target.Name)
	return nil
}

func UpsertServicePort(ports []core.ServicePort, x core.ServicePort) []core.ServicePort {
	for i, port := range ports {
		if port.Name == x.Name {
			port.Port = x.Port
			port.TargetPort = intstr.FromInt(int(x.Port))
			ports[i] = port
			return ports
		}
	}
	return append(ports, core.ServicePort{
		Name:       x.Name,
		Port:       x.Port,
		TargetPort: intstr.FromInt(int(x.Port)),
	})
}

func (r *FederationReconciler) copyEndpoints(srcSvc *core.Service, srcEP *core.Endpoints, targetSvcMon *monitoringv1.ServiceMonitor) error {
	target := core.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      srcEP.Name,
			Namespace: targetSvcMon.Namespace,
		},
	}

	vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &target, func(in client.Object, createOp bool) client.Object {
		obj := in.(*core.Endpoints)

		ref := metav1.NewControllerRef(targetSvcMon, schema.GroupVersionKind{
			Group:   monitoring.GroupName,
			Version: monitoringv1.Version,
			Kind:    "ServiceMonitor",
		})
		obj.OwnerReferences = []metav1.OwnerReference{*ref}
		obj.Labels = meta_util.OverwriteKeys(obj.Labels, srcEP.Labels)
		obj.Annotations = meta_util.OverwriteKeys(obj.Annotations, srcEP.Annotations)

		for i, srcSubNet := range srcEP.Subsets {
			if i >= len(obj.Subsets) {
				obj.Subsets = append(obj.Subsets, core.EndpointSubset{})
			}

			obj.Subsets[i].Addresses = []core.EndpointAddress{
				{
					IP: srcSvc.Spec.ClusterIP,
				},
			}
			for _, port := range srcSubNet.Ports {
				obj.Subsets[i].Ports = UpsertEndpointPort(obj.Subsets[i].Ports, core.EndpointPort{
					Name: port.Name,
					Port: ServicePortForName(srcSvc, port.Name),
				})
			}
		}

		return obj
	})
	if err != nil {
		return err
	}
	klog.Infof("%s Endpoints %s/%s", vt, target.Namespace, target.Name)
	return nil
}

func ServicePortForName(svc *core.Service, portName string) int32 {
	for _, port := range svc.Spec.Ports {
		if port.Name == portName {
			return port.Port
		}
	}
	panic(fmt.Errorf("service %s/%s has no port with name %s", svc.Namespace, svc.Name, portName))
}

func UpsertEndpointPort(ports []core.EndpointPort, x core.EndpointPort) []core.EndpointPort {
	for i, port := range ports {
		if port.Name == x.Name {
			port.Port = x.Port
			ports[i] = port
			return ports
		}
	}
	return append(ports, core.EndpointPort{
		Name: x.Name,
		Port: x.Port,
	})
}

// service -> []serviceMonitors
func (r *FederationReconciler) ServiceMonitorsForService(obj client.Object) []reconcile.Request {
	var list monitoringv1.ServiceMonitorList
	err := r.kc.List(context.TODO(), &list, client.MatchingLabels{
		mona.PrometheusKey: mona.PrometheusValueFederated,
	})
	if err != nil {
		klog.Error(err)
		return nil
	}

	var req []reconcile.Request
	for _, svcMon := range list.Items {
		nsMatches := svcMon.Spec.NamespaceSelector.Any ||
			contains(svcMon.Spec.NamespaceSelector.MatchNames, obj.GetNamespace())
		if !nsMatches {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(&svcMon.Spec.Selector)
		if err != nil {
			return nil
		}
		if sel.Matches(labels.Set(obj.GetLabels())) {
			req = append(req, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(svcMon)})
		}
	}
	return req
}

// Prometheus -> serviceMonitor
func ServiceMonitorsForPrometheus(kc client.Client, labels map[string]string) func(_ client.Object) []reconcile.Request {
	return func(_ client.Object) []reconcile.Request {
		var list monitoringv1.ServiceMonitorList
		err := kc.List(context.TODO(), &list, client.MatchingLabels(labels))
		if err != nil {
			klog.Error(err)
			return nil
		}

		var req []reconcile.Request
		for _, svcMon := range list.Items {
			req = append(req, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(svcMon)})
		}
		return req
	}
}

func contains(arr []string, x string) bool {
	for _, s := range arr {
		if s == x {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *FederationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&monitoringv1.ServiceMonitor{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			val, found := obj.GetLabels()[mona.PrometheusKey]
			return found && val == mona.PrometheusValueFederated
		}))).
		Watches(
			&source.Kind{Type: &core.Service{}},
			handler.EnqueueRequestsFromMapFunc(r.ServiceMonitorsForService)).
		Watches(
			&source.Kind{Type: &monitoringv1.Prometheus{}},
			handler.EnqueueRequestsFromMapFunc(ServiceMonitorsForPrometheus(r.kc, map[string]string{
				mona.PrometheusKey: mona.PrometheusValueFederated,
			}))).
		Complete(r)
}
