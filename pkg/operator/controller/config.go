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

package controller

import (
	"time"

	cs "go.openviz.dev/grafana-tools/client/clientset/versioned"
	grafanainformers "go.openviz.dev/grafana-tools/client/informers/externalversions"
	"go.openviz.dev/grafana-tools/pkg/operator/eventer"

	pcm "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned/typed/monitoring/v1"
	core "k8s.io/api/core/v1"
	crd_cs "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	clientsetscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	reg_util "kmodules.xyz/client-go/admissionregistration/v1"
	"kmodules.xyz/client-go/discovery"
	appcat_cs "kmodules.xyz/custom-resources/client/clientset/versioned/typed/appcatalog/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

const (
	mutatingWebhook   = "mutators.openviz.dev"
	validatingWebhook = "validators.openviz.dev"
)

type config struct {
	MaxNumRequeues          int
	NumThreads              int
	ResyncPeriod            time.Duration
	EnableValidatingWebhook bool
	EnableMutatingWebhook   bool
}

type Config struct {
	config

	ClientConfig     *rest.Config
	KubeClient       kubernetes.Interface
	ExtClient        cs.Interface
	CRDClient        crd_cs.Interface
	AppCatalogClient appcat_cs.AppcatalogV1alpha1Interface
	PromClient       pcm.MonitoringV1Interface
}

func NewConfig(clientConfig *rest.Config) *Config {
	return &Config{
		ClientConfig: clientConfig,
	}
}

func (c *Config) New() (*GrafanaController, error) {
	if err := discovery.IsDefaultSupportedVersion(c.KubeClient); err != nil {
		return nil, err
	}

	mapper, err := apiutil.NewDynamicRESTMapper(c.ClientConfig)
	if err != nil {
		return nil, err
	}
	cc, err := client.New(c.ClientConfig, client.Options{
		Scheme: clientsetscheme.Scheme,
		Mapper: mapper,
	})
	if err != nil {
		return nil, err
	}

	ctrl := &GrafanaController{
		config:           c.config,
		clientConfig:     c.ClientConfig,
		cc:               cc,
		kubeClient:       c.KubeClient,
		extClient:        c.ExtClient,
		crdClient:        c.CRDClient,
		promClient:       c.PromClient,
		appCatalogClient: c.AppCatalogClient,
		kubeInformerFactory: informers.NewSharedInformerFactoryWithOptions(
			c.KubeClient,
			c.ResyncPeriod,
			informers.WithNamespace(core.NamespaceAll)),
		extInformerFactory: grafanainformers.NewSharedInformerFactory(c.ExtClient, c.ResyncPeriod),
		recorder:           eventer.NewEventRecorder(c.KubeClient, "grafana-tools"),
	}

	if err := ctrl.ensureCustomResourceDefinitions(); err != nil {
		return nil, err
	}
	if c.EnableMutatingWebhook {
		if err := reg_util.UpdateMutatingWebhookCABundle(c.ClientConfig, mutatingWebhook); err != nil {
			return nil, err
		}
	}
	if c.EnableValidatingWebhook {
		if err := reg_util.UpdateValidatingWebhookCABundle(c.ClientConfig, validatingWebhook); err != nil {
			return nil, err
		}
	}

	// For GrafanaDashboard
	ctrl.initGrafanaDashboardWatcher()

	// For GrafanaDatasource
	ctrl.initGrafanaDatasourceWatcher()

	return ctrl, nil
}
