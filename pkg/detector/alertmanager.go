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
	"k8s.io/klog/v2"
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
	OpenShiftManaged() bool
	RancherManaged() bool
	Federated() bool
	IsDefault(am types.NamespacedName) bool
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
				l.delegated = &rancherAlertmanager{} // Rancher style rancherAlertmanager
				return nil
			}
		}

		// rancher cluster but using alertmanager directly
		l.delegated = &standaloneAlertmanager{openshift: false, rancher: true, singleton: len(list.Items) == 1}
		return nil
	}

	if clustermeta.IsOpenShiftManaged(l.kc.RESTMapper()) {
		for _, obj := range list.Items {
			if obj.GetNamespace() == clustermeta.OpenShiftUserWorkloadMonitoringNamespace &&
				obj.GetName() == clustermeta.OpenShiftUserWorkloadAlertmanager {
				l.delegated = &openshiftAlertmanager{} // OpenShift style rancherAlertmanager
				return nil
			}
		}
		klog.Infof("missing Alertmanager %s/%s\n", clustermeta.OpenShiftUserWorkloadMonitoringNamespace, clustermeta.OpenShiftUserWorkloadAlertmanager)
		return errNotReady
	}

	// using alertmanager directly and not rancher managed
	l.delegated = &standaloneAlertmanager{openshift: false, rancher: false, singleton: len(list.Items) == 1}
	return nil
}

func (l *lazyAlertmanager) Ready() (bool, error) {
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

func (l *lazyAlertmanager) OpenShiftManaged() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic(errNotReady)
	}
	return l.delegated.OpenShiftManaged()
}

func (l *lazyAlertmanager) RancherManaged() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic(errNotReady)
	}
	return l.delegated.RancherManaged()
}

func (l *lazyAlertmanager) Federated() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic(errNotReady)
	}
	return l.delegated.Federated()
}

func (l *lazyAlertmanager) IsDefault(am types.NamespacedName) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.delegated == nil {
		panic(errNotReady)
	}
	return l.delegated.IsDefault(am)
}

type openshiftAlertmanager struct{}

var _ AlertmanagerDetector = openshiftAlertmanager{}

func (f openshiftAlertmanager) Ready() (bool, error) {
	return true, nil
}

func (f openshiftAlertmanager) OpenShiftManaged() bool {
	return true
}

func (f openshiftAlertmanager) RancherManaged() bool {
	return false
}

func (f openshiftAlertmanager) Federated() bool {
	return false
}

func (f openshiftAlertmanager) IsDefault(am types.NamespacedName) bool {
	return am.Namespace == clustermeta.OpenShiftUserWorkloadMonitoringNamespace &&
		am.Name == clustermeta.OpenShiftUserWorkloadAlertmanager
}

type rancherAlertmanager struct{}

var _ AlertmanagerDetector = rancherAlertmanager{}

func (f rancherAlertmanager) Ready() (bool, error) {
	return true, nil
}

func (f rancherAlertmanager) OpenShiftManaged() bool {
	return false
}

func (f rancherAlertmanager) RancherManaged() bool {
	return true
}

func (f rancherAlertmanager) Federated() bool {
	return true
}

func (f rancherAlertmanager) IsDefault(am types.NamespacedName) bool {
	return am.Namespace == clustermeta.RancherMonitoringNamespace &&
		am.Name == clustermeta.RancherMonitoringAlertmanager
}

type standaloneAlertmanager struct {
	openshift bool
	rancher   bool
	singleton bool
}

var _ AlertmanagerDetector = standaloneAlertmanager{}

func (s standaloneAlertmanager) Ready() (bool, error) {
	return true, nil
}

func (s standaloneAlertmanager) OpenShiftManaged() bool {
	return s.openshift
}

func (s standaloneAlertmanager) RancherManaged() bool {
	return s.rancher
}

func (s standaloneAlertmanager) Federated() bool {
	return false
}

func (s standaloneAlertmanager) IsDefault(types.NamespacedName) bool {
	return s.singleton
}
