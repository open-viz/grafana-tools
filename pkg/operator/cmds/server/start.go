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

package server

import (
	"context"
	"fmt"
	"io"
	"net"

	openvizapi "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"

	"go.openviz.dev/grafana-tools/pkg/operator/server"
	"go.openviz.dev/grafana-tools/pkg/ui-server/apiserver"

	"github.com/spf13/pflag"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	"k8s.io/apiserver/pkg/features"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"k8s.io/apiserver/pkg/util/feature"
	"kmodules.xyz/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
)

const defaultEtcdPathPrefix = "/registry/openviz.dev"

type GrafanaOperatorOptions struct {
	RecommendedOptions *genericoptions.RecommendedOptions
	ExtraOptions       *ExtraOptions

	StdOut io.Writer
	StdErr io.Writer
}

func NewGrafanaDashboardOptions(out, errOut io.Writer) *GrafanaOperatorOptions {
	_ = feature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=false", features.APIPriorityAndFairness))
	o := &GrafanaOperatorOptions{
		// TODO we will nil out the etcd storage options.  This requires a later level of k8s.io/apiserver
		RecommendedOptions: genericoptions.NewRecommendedOptions(
			defaultEtcdPathPrefix,
			server.Codecs.LegacyCodec(admissionv1beta1.SchemeGroupVersion),
		),
		ExtraOptions: NewExtraOptions(),
		StdOut:       out,
		StdErr:       errOut,
	}
	o.RecommendedOptions.Etcd = nil
	o.RecommendedOptions.Admission = nil

	return o
}

func (o GrafanaOperatorOptions) AddFlags(fs *pflag.FlagSet) {
	o.RecommendedOptions.AddFlags(fs)
	o.ExtraOptions.AddFlags(fs)
}

func (o GrafanaOperatorOptions) Validate(args []string) error {
	return nil
}

func (o *GrafanaOperatorOptions) Complete() error {
	return nil
}

func (o GrafanaOperatorOptions) Config() (*server.GrafanaOperatorConfig, error) {
	// TODO have a "real" external address
	if err := o.RecommendedOptions.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
		return nil, fmt.Errorf("error creating self-signed certificates: %v", err)
	}

	serverConfig := genericapiserver.NewRecommendedConfig(apiserver.Codecs)
	if err := o.RecommendedOptions.ApplyTo(serverConfig); err != nil {
		return nil, err
	}
	// Fixes https://github.com/Azure/AKS/issues/522
	clientcmd.Fix(serverConfig.ClientConfig)

	extraConfig := NewConfig(serverConfig.ClientConfig)
	if err := o.ExtraOptions.ApplyTo(extraConfig); err != nil {
		return nil, err
	}

	serverConfig.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(
		openvizapi.GetOpenAPIDefinitions,
		openapi.NewDefinitionNamer(server.Scheme))
	serverConfig.OpenAPIConfig.Info.Title = "grafana-operator"
	serverConfig.OpenAPIConfig.Info.Version = "v0.0.1"

	cfg := &server.GrafanaOperatorConfig{
		GenericConfig: serverConfig,
		ExtraConfig: server.ExtraConfig{
			ClientConfig: serverConfig.ClientConfig,
		},
	}
	return cfg, nil
}

var (
	setupLog = ctrl.Log.WithName("setup")
)

func (o GrafanaOperatorOptions) Run(ctx context.Context) error {
	config, err := o.Config()
	if err != nil {
		return err
	}

	s, err := config.Complete().New(ctx)
	if err != nil {
		return err
	}

	setupLog.Info("starting manager")
	return s.Manager.Start(ctx)
}
