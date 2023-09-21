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

	openvizinstall "go.openviz.dev/apimachinery/apis/openviz/install"
	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	"go.openviz.dev/apimachinery/apis/ui"
	uiinstall "go.openviz.dev/apimachinery/apis/ui/install"
	uiapi "go.openviz.dev/apimachinery/apis/ui/v1alpha1"
	promtehsucontroller "go.openviz.dev/grafana-tools/pkg/controllers/prometheus"
	dashgroupstorage "go.openviz.dev/grafana-tools/pkg/registry/ui/dashboardgroup"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"
	"kmodules.xyz/authorizer"
	clustermeta "kmodules.xyz/client-go/cluster"
	appcatalogapi "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	"kmodules.xyz/resource-metadata/client/clientset/versioned"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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
	utilruntime.Must(appcatalogapi.AddToScheme(Scheme))
	utilruntime.Must(appcatalogapi.AddToScheme(Scheme))
	utilruntime.Must(chartsapi.AddToScheme(Scheme))

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
	ClientConfig *restclient.Config
	BaseURL      string
	Token        string
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

	c.GenericConfig.Version = &version.Info{
		Major: "1",
		Minor: "0",
	}

	return CompletedConfig{&c}
}

// New returns a new instance of UIServer from the given config.
func (c completedConfig) New(ctx context.Context) (*UIServer, error) {
	genericServer, err := c.GenericConfig.New("ui-server", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}

	// ctrl.SetLogger(...)
	log.SetLogger(klogr.New())

	mgr, err := manager.New(c.ExtraConfig.ClientConfig, manager.Options{
		Scheme:                 Scheme,
		MetricsBindAddress:     "",
		Port:                   0,
		HealthProbeBindAddress: "",
		LeaderElection:         false,
		LeaderElectionID:       "5b87adeb.ui.openviz.dev",
		ClientDisableCacheFor: []client.Object{
			&core.Namespace{},
			&core.Secret{},
			&core.Pod{},
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

	bc, err := promtehsucontroller.NewClient(c.ExtraConfig.BaseURL, c.ExtraConfig.Token, cid)
	if err != nil {
		return nil, err
	}

	if err = promtehsucontroller.NewReconciler(
		c.ExtraConfig.ClientConfig,
		versioned.NewForConfigOrDie(c.ExtraConfig.ClientConfig),
		mgr.GetClient(),
		bc,
	).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create controller", "controller", "Prometheus")
		os.Exit(1)
	}

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

	rbacAuthorizer := authorizer.NewForManagerOrDie(ctx, mgr)

	s := &UIServer{
		GenericAPIServer: genericServer,
		Manager:          mgr,
	}

	{
		apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(ui.GroupName, Scheme, metav1.ParameterCodec, Codecs)

		v1alpha1storage := map[string]rest.Storage{}
		v1alpha1storage[uiapi.ResourceDashboardGroups] = dashgroupstorage.NewStorage(ctrlClient, rbacAuthorizer)
		apiGroupInfo.VersionedResourcesStorageMap["v1alpha1"] = v1alpha1storage

		if err := s.GenericAPIServer.InstallAPIGroup(&apiGroupInfo); err != nil {
			return nil, err
		}
	}

	return s, nil
}
