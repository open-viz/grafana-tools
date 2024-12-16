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
	"sync"

	"github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clustermeta "kmodules.xyz/client-go/cluster"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var GVKPrometheus = schema.GroupVersionKind{
	Group:   monitoring.GroupName,
	Version: monitoringv1.Version,
	Kind:    "Prometheus",
}

type PrometheusDetector interface {
	Ready() (bool, error)
	RancherManaged() bool
	Federated() bool
	IsDefault(key types.NamespacedName) bool
}

func NewPrometheusDetector(kc client.Client) PrometheusDetector {
	return &lazyPrometheus{kc: kc}
}

var errUnknown = errors.New("unknown")

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
				l.delegated = &federatedPrometheus{} // rancher style federatedPrometheus
				return nil
			}
		}

		// rancher cluster but using prometheus directly
		l.delegated = &standalonePrometheus{rancher: true, singleton: len(list.Items) == 1}
		return nil
	}

	// using prometheus directly and not rancher managed
	l.delegated = &standalonePrometheus{rancher: false, singleton: len(list.Items) == 1}
	return nil
}

func (l *lazyPrometheus) Ready() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		err := l.detect()
		return err == nil, err
	}
	return true, nil
}

func (l *lazyPrometheus) RancherManaged() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic("NotReady")
	}
	return l.delegated.RancherManaged()
}

func (l *lazyPrometheus) Federated() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic("NotReady")
	}
	return l.delegated.Federated()
}

func (l *lazyPrometheus) IsDefault(key types.NamespacedName) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic("NotReady")
	}
	return l.delegated.IsDefault(key)
}

type federatedPrometheus struct{}

var _ PrometheusDetector = federatedPrometheus{}

func (f federatedPrometheus) Ready() (bool, error) {
	return true, nil
}

func (f federatedPrometheus) RancherManaged() bool {
	return true
}

func (f federatedPrometheus) Federated() bool {
	return true
}

func (f federatedPrometheus) IsDefault(key types.NamespacedName) bool {
	return key.Namespace == clustermeta.RancherMonitoringNamespace &&
		key.Name == clustermeta.RancherMonitoringPrometheus
}

type standalonePrometheus struct {
	rancher   bool
	singleton bool
}

var _ PrometheusDetector = standalonePrometheus{}

func (s standalonePrometheus) Ready() (bool, error) {
	return true, nil
}

func (s standalonePrometheus) RancherManaged() bool {
	return s.rancher
}

func (s standalonePrometheus) Federated() bool {
	return false
}

func (s standalonePrometheus) IsDefault(key types.NamespacedName) bool {
	return s.singleton
}
