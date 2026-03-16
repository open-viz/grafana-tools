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

package detector

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	clustermeta "kmodules.xyz/client-go/cluster"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const portPrometheus = "http-web"

var GVKPrometheus = schema.GroupVersionKind{
	Group:   monitoring.GroupName,
	Version: monitoringv1.Version,
	Kind:    "Prometheus",
}

type PrometheusDetector interface {
	Ready() (bool, error)
	OpenShiftManaged() bool
	RancherManaged() bool
	Federated() bool
	IsDefault(prom types.NamespacedName) bool
	ServiceKey(prom types.NamespacedName) types.NamespacedName
	Service(prom types.NamespacedName, svc *core.Service) mona.ServiceSpec
}

func NewPrometheusDetector(kc client.Client) PrometheusDetector {
	return &lazyPrometheus{kc: kc}
}

var (
	errUnknown  = errors.New("unknown")
	errNotReady = errors.New("not ready")
)

type lazyPrometheus struct {
	kc        client.Client
	delegated PrometheusDetector
	mu        sync.Mutex
}

var _ PrometheusDetector = &lazyPrometheus{}

func (l *lazyPrometheus) detect() error {
	var list unstructured.UnstructuredList
	list.SetGroupVersionKind(GVKPrometheus)
	err := l.kc.List(context.TODO(), &list)
	if err != nil {
		return err
	}
	if len(list.Items) == 0 {
		return errUnknown
	}

	if clustermeta.IsRancherManaged(l.kc.RESTMapper()) {
		for _, obj := range list.Items {
			if obj.GetNamespace() == clustermeta.RancherMonitoringNamespace &&
				obj.GetName() == clustermeta.RancherMonitoringPrometheus {
				l.delegated = &rancherPrometheus{} // Rancher style rancherPrometheus
				return nil
			}
		}

		// rancher cluster but using prometheus directly
		l.delegated = &standalonePrometheus{openshift: false, rancher: true, singleton: len(list.Items) == 1}
		return nil
	}

	if clustermeta.IsOpenShiftManaged(l.kc.RESTMapper()) {
		for _, obj := range list.Items {
			if obj.GetNamespace() == clustermeta.OpenShiftUserWorkloadMonitoringNamespace &&
				obj.GetName() == clustermeta.OpenShiftUserWorkloadPrometheus {
				l.delegated = &openshiftPrometheus{} // OpenShift style rancherPrometheus
				return nil
			}
		}
		klog.Infof("missing Prometheus %s/%s\n", clustermeta.OpenShiftUserWorkloadMonitoringNamespace, clustermeta.OpenShiftUserWorkloadPrometheus)
		return errNotReady
	}

	// using prometheus directly and not rancher managed
	l.delegated = &standalonePrometheus{openshift: false, rancher: false, singleton: len(list.Items) == 1}
	return nil
}

func (l *lazyPrometheus) Ready() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		err := l.detect()
		if errors.Is(err, errNotReady) {
			return false, nil
		}
		return err == nil, err
	}
	return true, nil
}

func (l *lazyPrometheus) OpenShiftManaged() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic(errNotReady)
	}
	return l.delegated.OpenShiftManaged()
}

func (l *lazyPrometheus) RancherManaged() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic(errNotReady)
	}
	return l.delegated.RancherManaged()
}

func (l *lazyPrometheus) Federated() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic(errNotReady)
	}
	return l.delegated.Federated()
}

func (l *lazyPrometheus) IsDefault(prom types.NamespacedName) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic(errNotReady)
	}
	return l.delegated.IsDefault(prom)
}

func (l *lazyPrometheus) ServiceKey(prom types.NamespacedName) types.NamespacedName {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic(errNotReady)
	}
	return l.delegated.ServiceKey(prom)
}

func (l *lazyPrometheus) Service(prom types.NamespacedName, svc *core.Service) mona.ServiceSpec {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic(errNotReady)
	}
	return l.delegated.Service(prom, svc)
}

type openshiftPrometheus struct{}

var _ PrometheusDetector = openshiftPrometheus{}

func (f openshiftPrometheus) Ready() (bool, error) {
	return true, nil
}

func (f openshiftPrometheus) OpenShiftManaged() bool {
	return true
}

func (f openshiftPrometheus) RancherManaged() bool {
	return false
}

func (f openshiftPrometheus) Federated() bool {
	return false
}

func (f openshiftPrometheus) IsDefault(prom types.NamespacedName) bool {
	return prom.Namespace == clustermeta.OpenShiftUserWorkloadMonitoringNamespace &&
		prom.Name == clustermeta.OpenShiftUserWorkloadPrometheus
}

func (f openshiftPrometheus) ServiceKey(prom types.NamespacedName) types.NamespacedName {
	return types.NamespacedName{
		Namespace: clustermeta.OpenShiftClusterMonitoringNamespace,
		Name:      clustermeta.OpenShiftThanosQuerierService,
	}
}

func (f openshiftPrometheus) Service(_ types.NamespacedName, svc *core.Service) mona.ServiceSpec {
	spec := mona.ServiceSpec{
		Namespace: clustermeta.OpenShiftClusterMonitoringNamespace,
		Name:      clustermeta.OpenShiftThanosQuerierService,
		Scheme:    "https",
		Port:      "", // "9091", 9092
	}
	if port, found := findPort(svc, "web"); found {
		spec.Port = fmt.Sprintf("%d", port)
	}
	return spec
}

type rancherPrometheus struct{}

var _ PrometheusDetector = rancherPrometheus{}

func (f rancherPrometheus) Ready() (bool, error) {
	return true, nil
}

func (f rancherPrometheus) OpenShiftManaged() bool {
	return false
}

func (f rancherPrometheus) RancherManaged() bool {
	return true
}

func (f rancherPrometheus) Federated() bool {
	return true
}

func (f rancherPrometheus) IsDefault(prom types.NamespacedName) bool {
	return prom.Namespace == clustermeta.RancherMonitoringNamespace &&
		prom.Name == clustermeta.RancherMonitoringPrometheus
}

func (f rancherPrometheus) ServiceKey(prom types.NamespacedName) types.NamespacedName {
	return types.NamespacedName{
		Namespace: prom.Namespace,
		Name:      prom.Name,
	}
}

func (f rancherPrometheus) Service(prom types.NamespacedName, svc *core.Service) mona.ServiceSpec {
	spec := mona.ServiceSpec{
		Namespace: prom.Namespace,
		Name:      prom.Name,
		Scheme:    "http",
		Port:      "",
	}
	if port, found := findPort(svc, portPrometheus); found {
		spec.Port = fmt.Sprintf("%d", port)
	}
	return spec
}

type standalonePrometheus struct {
	openshift bool
	rancher   bool
	singleton bool
}

var _ PrometheusDetector = standalonePrometheus{}

func (s standalonePrometheus) Ready() (bool, error) {
	return true, nil
}

func (s standalonePrometheus) OpenShiftManaged() bool {
	return s.openshift
}

func (s standalonePrometheus) RancherManaged() bool {
	return s.rancher
}

func (s standalonePrometheus) Federated() bool {
	return false
}

func (s standalonePrometheus) IsDefault(types.NamespacedName) bool {
	return s.singleton
}

func (s standalonePrometheus) ServiceKey(prom types.NamespacedName) types.NamespacedName {
	return types.NamespacedName{
		Namespace: prom.Namespace,
		Name:      prom.Name,
	}
}

func (s standalonePrometheus) Service(prom types.NamespacedName, svc *core.Service) mona.ServiceSpec {
	spec := mona.ServiceSpec{
		Namespace: prom.Namespace,
		Name:      prom.Name,
		Scheme:    "http",
		Port:      "",
	}
	if port, found := findPort(svc, portPrometheus); found {
		spec.Port = fmt.Sprintf("%d", port)
	}
	return spec
}

func DefaultPrometheus(mapper meta.RESTMapper, d PrometheusDetector, prometheuses []monitoringv1.Prometheus) (*monitoringv1.Prometheus, bool) {
	if len(prometheuses) == 0 {
		return nil, false
	}
	if clustermeta.IsOpenShiftManaged(mapper) {
		if idx := slices.IndexFunc(prometheuses, func(p monitoringv1.Prometheus) bool {
			return d.IsDefault(client.ObjectKeyFromObject(&p))
		}); idx == -1 {
			return nil, false
		} else {
			return &prometheuses[idx], true
		}
	}
	return &prometheuses[0], true
}

func findPort(svc *core.Service, portName string) (int32, bool) {
	for _, p := range svc.Spec.Ports {
		if p.Name == portName {
			return p.Port, true
		}
	}
	return -1, false
}
