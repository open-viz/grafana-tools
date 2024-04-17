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

type Detector interface {
	Ready() (bool, error)
	RancherManaged_() bool
	Federated() bool
	IsDefault(key types.NamespacedName) bool
}

func New(kc client.Client) Detector {
	return &lazy{kc: kc}
}

var errUnknown = errors.New("unknown")

type lazy struct {
	kc        client.Client
	delegated Detector
	mu        sync.Mutex
}

var _ Detector = &lazy{}

func (l *lazy) detect() error {
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
			if obj.GetNamespace() == clustermeta.NamespaceRancherMonitoring &&
				obj.GetName() == clustermeta.PrometheusRancherMonitoring {
				l.delegated = &federated{} // rancher style federated
				return nil
			}
		}

		// rancher cluster but using prometheus directly
		l.delegated = &standalone{rancher: true, singleton: len(list.Items) == 1}
		return nil
	}

	// using prometheus directly and not rancher managed
	l.delegated = &standalone{rancher: false, singleton: len(list.Items) == 1}
	return nil
}

func (l *lazy) Ready() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		err := l.detect()
		return err == nil, err
	}
	return true, nil
}

func (l *lazy) RancherManaged_() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic("NotReady")
	}
	return l.delegated.RancherManaged_()
}

func (l *lazy) Federated() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic("NotReady")
	}
	return l.delegated.Federated()
}

func (l *lazy) IsDefault(key types.NamespacedName) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic("NotReady")
	}
	return l.delegated.IsDefault(key)
}

type federated struct{}

var _ Detector = federated{}

func (f federated) Ready() (bool, error) {
	return true, nil
}

func (f federated) RancherManaged_() bool {
	return true
}

func (f federated) Federated() bool {
	return true
}

func (f federated) IsDefault(key types.NamespacedName) bool {
	return key.Namespace == clustermeta.NamespaceRancherMonitoring &&
		key.Name == clustermeta.PrometheusRancherMonitoring
}

type standalone struct {
	rancher   bool
	singleton bool
}

var _ Detector = standalone{}

func (s standalone) Ready() (bool, error) {
	return true, nil
}

func (s standalone) RancherManaged_() bool {
	return s.rancher
}

func (s standalone) Federated() bool {
	return false
}

func (s standalone) IsDefault(key types.NamespacedName) bool {
	return s.singleton
}
