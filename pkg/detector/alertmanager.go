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
	"sync"

	"github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clustermeta "kmodules.xyz/client-go/cluster"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var GVKAlertmanager = schema.GroupVersionKind{
	Group:   monitoring.GroupName,
	Version: monitoringv1.Version,
	Kind:    "Alertmanager",
}

type AlertmanagerDetector interface {
	Ready() (bool, error)
	RancherManaged() bool
	Federated() bool
	IsDefault(key types.NamespacedName) bool
}

func NewAlertmanagerDetector(kc client.Client) AlertmanagerDetector {
	return &lazyAlertmanager{kc: kc}
}

type lazyAlertmanager struct {
	kc        client.Client
	delegated AlertmanagerDetector
	mu        sync.Mutex
}

var _ AlertmanagerDetector = &lazyAlertmanager{}

func (l *lazyAlertmanager) detect() error {
	var list unstructured.UnstructuredList
	list.SetGroupVersionKind(GVKAlertmanager)
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
				obj.GetName() == clustermeta.RancherMonitoringAlertmanager {
				l.delegated = &federatedAlertmanager{} // rancher style federatedAlertmanager
				return nil
			}
		}

		// rancher cluster but using alertmanager directly
		l.delegated = &standaloneAlertmanager{rancher: true, singleton: len(list.Items) == 1}
		return nil
	}

	// using alertmanager directly and not rancher managed
	l.delegated = &standaloneAlertmanager{rancher: false, singleton: len(list.Items) == 1}
	return nil
}

func (l *lazyAlertmanager) Ready() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		err := l.detect()
		return err == nil, err
	}
	return true, nil
}

func (l *lazyAlertmanager) RancherManaged() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic("NotReady")
	}
	return l.delegated.RancherManaged()
}

func (l *lazyAlertmanager) Federated() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic("NotReady")
	}
	return l.delegated.Federated()
}

func (l *lazyAlertmanager) IsDefault(key types.NamespacedName) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic("NotReady")
	}
	return l.delegated.IsDefault(key)
}

type federatedAlertmanager struct{}

var _ AlertmanagerDetector = federatedAlertmanager{}

func (f federatedAlertmanager) Ready() (bool, error) {
	return true, nil
}

func (f federatedAlertmanager) RancherManaged() bool {
	return true
}

func (f federatedAlertmanager) Federated() bool {
	return true
}

func (f federatedAlertmanager) IsDefault(key types.NamespacedName) bool {
	return key.Namespace == clustermeta.RancherMonitoringNamespace &&
		key.Name == clustermeta.RancherMonitoringAlertmanager
}

type standaloneAlertmanager struct {
	rancher   bool
	singleton bool
}

var _ AlertmanagerDetector = standaloneAlertmanager{}

func (s standaloneAlertmanager) Ready() (bool, error) {
	return true, nil
}

func (s standaloneAlertmanager) RancherManaged() bool {
	return s.rancher
}

func (s standaloneAlertmanager) Federated() bool {
	return false
}

func (s standaloneAlertmanager) IsDefault(key types.NamespacedName) bool {
	return s.singleton
}
