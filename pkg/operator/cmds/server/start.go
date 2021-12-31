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
	"flag"
	"fmt"
	"io"
	"os"

	openvizv1alpha1 "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	openvizcontrollers "go.openviz.dev/grafana-tools/pkg/operator/controllers/openviz"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apiserver/pkg/features"
	"k8s.io/apiserver/pkg/util/feature"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type GrafanaDashboardOptions struct {
	//RecommendedOptions *genericoptions.RecommendedOptions
	ExtraOptions *ExtraOptions

	StdOut io.Writer
	StdErr io.Writer
}

func NewGrafanaDashboardOptions(out, errOut io.Writer) *GrafanaDashboardOptions {
	_ = feature.DefaultMutableFeatureGate.Set(fmt.Sprintf("%s=false", features.APIPriorityAndFairness))
	o := &GrafanaDashboardOptions{
		// TODO we will nil out the etcd storage options.  This requires a later level of k8s.io/apiserver
		//RecommendedOptions: genericoptions.NewRecommendedOptions(
		//	defaultEtcdPathPrefix,
		//	server.Codecs.LegacyCodec(admissionv1beta1.SchemeGroupVersion),
		//),
		ExtraOptions: NewExtraOptions(),
		StdOut:       out,
		StdErr:       errOut,
	}
	//o.RecommendedOptions.Etcd = nil
	//o.RecommendedOptions.Admission = nil

	return o
}

func (o GrafanaDashboardOptions) AddFlags(fs *pflag.FlagSet) {
	//o.RecommendedOptions.AddFlags(fs)
	o.ExtraOptions.AddFlags(fs)
}

func (o GrafanaDashboardOptions) Validate(args []string) error {
	return nil
}

func (o *GrafanaDashboardOptions) Complete() error {
	return nil
}

//
//func (o GrafanaDashboardOptions) Config() (*server.GrafanaOperatorConfig, error) {
//	// TODO have a "real" external address
//	if err := o.RecommendedOptions.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
//		return nil, fmt.Errorf("error creating self-signed certificates: %v", err)
//	}
//
//	serverConfig := genericapiserver.NewRecommendedConfig(server.Codecs)
//	serverConfig.EnableMetrics = true
//	if err := o.RecommendedOptions.ApplyTo(serverConfig); err != nil {
//		return nil, err
//	}
//	// Fixes https://github.com/Azure/AKS/issues/522
//	clientcmd.Fix(serverConfig.ClientConfig)
//
//	extraConfig := controller.NewConfig(serverConfig.ClientConfig)
//	if err := o.ExtraOptions.ApplyTo(extraConfig); err != nil {
//		return nil, err
//	}
//
//	config := &server.GrafanaOperatorConfig{
//		GenericConfig: serverConfig,
//		ExtraConfig:   extraConfig,
//	}
//	return config, nil
//}

var (
	scheme               = runtime.NewScheme()
	setupLog             = ctrl.Log.WithName("setup")
	metricsAddr          string
	enableLeaderElection bool
	probeAddr            string
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(appcatalog.AddToScheme(scheme))
	utilruntime.Must(openvizv1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
}

func GetManager() (manager.Manager, error) {
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "d2899bc8.appscode.com",
	})
	if err != nil {
		return nil, err
	}
	return mgr, nil
}

func (o GrafanaDashboardOptions) Run(stopCh <-chan struct{}) error {
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := GetManager()
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&openvizcontrollers.GrafanaDashboardReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("grafana-dashboard-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "GrafanaDashboard")
		os.Exit(1)
	}
	if err = (&openvizcontrollers.GrafanaDatasourceReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("grafana-datasource-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "GrafanaDatasource")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
	return nil
}
