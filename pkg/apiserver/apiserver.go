/*
Copyright AppsCode Inc. and Contributors.

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

package apiserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	v1 "github.com/perses/perses/pkg/model/api/v1"
	openvizinstall "go.openviz.dev/apimachinery/apis/openviz/install"
	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	"go.openviz.dev/apimachinery/apis/ui"
	uiinstall "go.openviz.dev/apimachinery/apis/ui/install"
	uiapi "go.openviz.dev/apimachinery/apis/ui/v1alpha1"
	alertmanagercontroller "go.openviz.dev/grafana-tools/pkg/controllers/alertmanager"
	namespacecontroller "go.openviz.dev/grafana-tools/pkg/controllers/namespace"
	prometheuscontroller "go.openviz.dev/grafana-tools/pkg/controllers/prometheus"
	"go.openviz.dev/grafana-tools/pkg/controllers/ranchertoken"
	servicemonitorcontroller "go.openviz.dev/grafana-tools/pkg/controllers/servicemonitor"
	"go.openviz.dev/grafana-tools/pkg/detector"
	dashgroupstorage "go.openviz.dev/grafana-tools/pkg/registry/ui/dashboardgroup"

	"github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	core "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"kmodules.xyz/authorizer"
	"kmodules.xyz/client-go/apiextensions"
	clustermeta "kmodules.xyz/client-go/cluster"
	appcatalogapi "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	chartsapi "x-helm.dev/apimachinery/apis/charts/v1alpha1"
)

var (
	// Scheme defines methods for serializing and deserializing API objects.
	Scheme = runtime.NewScheme()
	// Codecs provides methods for retrieving codecs and serializers for specific
	// versions and content types.
	Codecs = serializer.NewCodecFactory(Scheme)
)

func init() {
	uiinstall.Install(Scheme)
	openvizinstall.Install(Scheme)
	utilruntime.Must(clientgoscheme.AddToScheme(Scheme))
	utilruntime.Must(apiregistrationv1.AddToScheme(Scheme))
	utilruntime.Must(monitoringv1.AddToScheme(Scheme))
	utilruntime.Must(monitoringv1alpha1.AddToScheme(Scheme))
	utilruntime.Must(appcatalogapi.AddToScheme(Scheme))
	utilruntime.Must(chartsapi.AddToScheme(Scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(Scheme))

	// we need to add the options to empty v1
	// TODO fix the server code to avoid this
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})

	// TODO: keep the generic API server from wanting this
	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	Scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	)
}

// ExtraConfig holds custom apiserver config
type ExtraConfig struct {
	ClientConfig      *restclient.Config
	BaseURL           string
	Token             string
	CACert            []byte
	HubUID            string
	RancherAuthSecret string
}

// Config defines the config for the apiserver
type Config struct {
	GenericConfig *genericapiserver.RecommendedConfig
	ExtraConfig   ExtraConfig
}

// UIServer contains state for a Kubernetes cluster master/api server.
type UIServer struct {
	GenericAPIServer *genericapiserver.GenericAPIServer
	Manager          manager.Manager
}

type completedConfig struct {
	GenericConfig genericapiserver.CompletedConfig
	ExtraConfig   *ExtraConfig
}

// CompletedConfig embeds a private pointer that cannot be instantiated outside of this package.
type CompletedConfig struct {
	*completedConfig
}

// Complete fills in any fields not set that are required to have valid data. It's mutating the receiver.
func (cfg *Config) Complete() CompletedConfig {
	c := completedConfig{
		cfg.GenericConfig.Complete(),
		&cfg.ExtraConfig,
	}
	return CompletedConfig{&c}
}

// New returns a new instance of UIServer from the given config.
func (c completedConfig) New(ctx context.Context) (*UIServer, error) {
	genericServer, err := c.GenericConfig.New("ui-server", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}

	log.SetLogger(klog.NewKlogr())

	mgr, err := ctrl.NewManager(c.ExtraConfig.ClientConfig, ctrl.Options{
		Scheme:                 Scheme,
		Metrics:                metricsserver.Options{BindAddress: ""},
		HealthProbeBindAddress: "",
		LeaderElection:         false,
		LeaderElectionID:       "5b87adeb.ui.openviz.dev",
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{
					&core.Pod{},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("unable to start manager, reason: %v", err)
	}
	ctrlClient := mgr.GetClient()

	cid, err := clustermeta.ClusterUID(mgr.GetAPIReader())
	if err != nil {
		return nil, err
	}

	var bc *prometheuscontroller.Client
	if c.ExtraConfig.BaseURL != "" && c.ExtraConfig.Token != "" {
		bc, err = prometheuscontroller.NewClient(c.ExtraConfig.BaseURL, c.ExtraConfig.Token, c.ExtraConfig.CACert)
		if err != nil {
			return nil, err
		}
	}

	if err := apiextensions.NewReconciler(ctx, mgr).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create controller controller CustomResourceReconciler")
		os.Exit(1)
	}

	promDetector := detector.NewPrometheusDetector(mgr.GetClient())
	amgrDetector := detector.NewAlertmanagerDetector(mgr.GetClient())

	if c.ExtraConfig.RancherAuthSecret != "" {
		if err = ranchertoken.NewTokenRefresher(
			mgr.GetClient(),
			c.ExtraConfig.RancherAuthSecret,
		).SetupWithManager(mgr); err != nil {
			klog.Error(err, "unable to create token refresher", "controller", "TokenRefresher")
			os.Exit(1)
		}
	}

	apiextensions.RegisterSetup(schema.GroupKind{
		Group: monitoring.GroupName,
		Kind:  monitoringv1.PrometheusesKind,
	}, func(ctx context.Context, mgr ctrl.Manager) {
		if err = namespacecontroller.NewReconciler(
			mgr.GetClient(),
			bc,
			cid,
			c.ExtraConfig.HubUID,
			promDetector,
		).SetupWithManager(mgr); err != nil {
			klog.Error(err, "unable to create controller", "controller", "ClientOrg")
			os.Exit(1)
		}

		if err = prometheuscontroller.NewReconciler(
			mgr.GetClient(),
			bc,
			cid,
			c.ExtraConfig.HubUID,
			c.ExtraConfig.RancherAuthSecret,
			promDetector,
		).SetupWithManager(mgr); err != nil {
			klog.Error(err, "unable to create controller", "controller", "Prometheus")
			os.Exit(1)
		}
	})

	apiextensions.RegisterSetup(schema.GroupKind{
		Group: monitoring.GroupName,
		Kind:  monitoringv1.AlertmanagersKind,
	}, func(ctx context.Context, mgr ctrl.Manager) {
		if err = alertmanagercontroller.NewReconciler(
			mgr.GetClient(),
			cid,
			amgrDetector,
		).SetupWithManager(mgr); err != nil {
			klog.Error(err, "unable to create controller", "controller", "Alertmanagers")
			os.Exit(1)
		}
	})

	apiextensions.RegisterSetup(schema.GroupKind{
		Group: monitoring.GroupName,
		Kind:  monitoringv1.ServiceMonitorsKind,
	}, func(ctx context.Context, mgr ctrl.Manager) {
		if err = servicemonitorcontroller.NewAutoReconciler(
			c.ExtraConfig.ClientConfig,
			mgr.GetClient(),
		).SetupWithManager(mgr); err != nil {
			klog.Error(err, "unable to create controller", "auto controller", "ServiceMonitor")
			os.Exit(1)
		}

		if err = servicemonitorcontroller.NewFederationReconciler(
			c.ExtraConfig.ClientConfig,
			mgr.GetClient(),
			promDetector,
		).SetupWithManager(mgr); err != nil {
			klog.Error(err, "unable to create controller", " federation controller", "ServiceMonitor")
			os.Exit(1)
		}
	})

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &appcatalogapi.AppBinding{}, mona.DefaultGrafanaKey, func(rawObj client.Object) []string {
		app := rawObj.(*appcatalogapi.AppBinding)
		if v, ok := app.Annotations[mona.DefaultGrafanaKey]; ok && v == "true" {
			return []string{"true"}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &openvizapi.GrafanaDashboard{}, mona.DefaultGrafanaKey, func(rawObj client.Object) []string {
		dashboard := rawObj.(*openvizapi.GrafanaDashboard)
		if dashboard.Spec.GrafanaRef == nil {
			return []string{"true"}
		} else {
			var app appcatalogapi.AppBinding
			if err := ctrlClient.Get(ctx, dashboard.Spec.GrafanaRef.ObjectKey(), &app); err == nil {
				if v, ok := app.Annotations[mona.DefaultGrafanaKey]; ok && v == "true" {
					return []string{"true"}
				}
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &openvizapi.GrafanaDashboard{}, openvizapi.GrafanaDashboardTitleKey, func(rawObj client.Object) []string {
		dashboard := rawObj.(*openvizapi.GrafanaDashboard)
		board := struct {
			Title string `json:"title"`
		}{}
		if dashboard.Spec.Model == nil {
			return nil
		}
		if err := json.Unmarshal(dashboard.Spec.Model.Raw, &board); err != nil {
			klog.ErrorS(err, "failed to unmarshal spec.model in GrafanaDashboard", "name", dashboard.Name, "namespace", dashboard.Namespace)
			return nil
		}
		return []string{board.Title}
	}); err != nil {
		return nil, err
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &appcatalogapi.AppBinding{}, mona.DefaultPersesKey, func(rawObj client.Object) []string {
		app := rawObj.(*appcatalogapi.AppBinding)
		if v, ok := app.Annotations[mona.DefaultPersesKey]; ok && v == "true" {
			return []string{"true"}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &openvizapi.PersesDashboard{}, mona.DefaultPersesKey, func(rawObj client.Object) []string {
		dashboard := rawObj.(*openvizapi.PersesDashboard)
		if dashboard.Spec.PersesRef == nil {
			return []string{"true"}
		} else {
			var app appcatalogapi.AppBinding
			if err := ctrlClient.Get(ctx, dashboard.Spec.PersesRef.ObjectKey(), &app); err == nil {
				if v, ok := app.Annotations[mona.DefaultPersesKey]; ok && v == "true" {
					return []string{"true"}
				}
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &openvizapi.PersesDashboard{}, openvizapi.PersesDashboardTitleKey, func(rawObj client.Object) []string {
		dashboard := rawObj.(*openvizapi.PersesDashboard)
		if dashboard.Spec.Model == nil {
			return nil
		}
		var persesDashboard v1.Dashboard
		if err := json.Unmarshal(dashboard.Spec.Model.Raw, &persesDashboard); err != nil {
			klog.ErrorS(err, "failed to unmarshal spec.model in GrafanaDashboard", "name", dashboard.Name, "namespace", dashboard.Namespace)
			return nil
		}
		return []string{persesDashboard.Spec.Display.Name}
	}); err != nil {
		return nil, err
	}

	rbacAuthorizer := authorizer.NewForManagerOrDie(ctx, mgr)

	s := &UIServer{
		GenericAPIServer: genericServer,
		Manager:          mgr,
	}

	{
		apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(ui.GroupName, Scheme, metav1.ParameterCodec, Codecs)

		v1alpha1storage := map[string]rest.Storage{}
		v1alpha1storage[uiapi.ResourceDashboardGroups] = dashgroupstorage.NewStorage(ctrlClient, rbacAuthorizer, promDetector)
		apiGroupInfo.VersionedResourcesStorageMap["v1alpha1"] = v1alpha1storage

		if err := s.GenericAPIServer.InstallAPIGroup(&apiGroupInfo); err != nil {
			return nil, err
		}
	}

	return s, nil
}
