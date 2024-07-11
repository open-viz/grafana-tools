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
	"flag"
	"os"
	"time"

	"go.openviz.dev/grafana-tools/pkg/apiserver"
	"go.openviz.dev/grafana-tools/pkg/controllers"
	"go.openviz.dev/grafana-tools/pkg/controllers/openviz"

	"github.com/spf13/pflag"
	v "gomodules.xyz/x/version"
	crd_cs "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"
	cu "kmodules.xyz/client-go/client"
	clustermeta "kmodules.xyz/client-go/cluster"
	"kmodules.xyz/client-go/tools/clientcmd"
	appcatalogapi "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const (
	defaultRequeueAfterDuration = time.Minute
)

var setupLog = log.Log.WithName("setup")

type OperatorOptions struct {
	MasterURL            string
	KubeconfigPath       string
	QPS                  float64
	Burst                int
	ResyncPeriod         time.Duration
	MaxNumRequeues       int
	NumThreads           int
	RequeueAfterDuration time.Duration

	metricsAddr          string
	enableLeaderElection bool
	probeAddr            string
}

func NewOperatorOptions() *OperatorOptions {
	return &OperatorOptions{
		ResyncPeriod:         10 * time.Minute,
		RequeueAfterDuration: defaultRequeueAfterDuration,
		MaxNumRequeues:       5,
		NumThreads:           2,
		// ref: https://github.com/kubernetes/ingress-nginx/blob/e4d53786e771cc6bdd55f180674b79f5b692e552/pkg/ingress/controller/launch.go#L252-L259
		// High enough QPS to fit all expected use cases. QPS=0 is not set here, because client code is overriding it.
		QPS: 1e6,
		// High enough Burst to fit all expected use cases. Burst=0 is not set here, because client code is overriding it.
		Burst:                1e6,
		metricsAddr:          ":8080",
		enableLeaderElection: false,
		probeAddr:            ":8081",
	}
}

func (s *OperatorOptions) AddGoFlags(fs *flag.FlagSet) {
	clustermeta.AddGoFlags(fs)
	fs.StringVar(&s.MasterURL, "master", s.MasterURL, "The address of the Kubernetes API server (overrides any value in kubeconfig)")
	fs.StringVar(&s.KubeconfigPath, "kubeconfig", s.KubeconfigPath, "Path to kubeconfig file with authorization information (the master location is set by the master flag).")

	fs.Float64Var(&s.QPS, "qps", s.QPS, "The maximum QPS to the master from this client")
	fs.IntVar(&s.Burst, "burst", s.Burst, "The maximum burst for throttle")
	fs.DurationVar(&s.ResyncPeriod, "resync-period", s.ResyncPeriod, "If non-zero, will re-list this often. Otherwise, re-list will be delayed aslong as possible (until the upstream source closes the watch or times out.")
	fs.DurationVar(&s.RequeueAfterDuration, "requeue-after-duration", s.RequeueAfterDuration, "Duration after the GrafanaDashboard object will be requeue when it is failed to create external grafana dashboard resource. The flag accepts a value acceptable to time.ParseDuration. Ref: https://pkg.go.dev/time#ParseDuration")

	fs.StringVar(&s.metricsAddr, "metrics-bind-address", s.metricsAddr, "The address the metric endpoint binds to.")
	fs.StringVar(&s.probeAddr, "health-probe-bind-address", s.probeAddr, "The address the probe endpoint binds to.")
	fs.BoolVar(&s.enableLeaderElection, "leader-elect", s.enableLeaderElection,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
}

func (s *OperatorOptions) AddFlags(fs *pflag.FlagSet) {
	pfs := flag.NewFlagSet("grafana-tools", flag.ExitOnError)
	s.AddGoFlags(pfs)
	fs.AddGoFlagSet(pfs)
}

func (s *OperatorOptions) Validate() []error {
	var errs []error
	if _, err := time.ParseDuration(s.RequeueAfterDuration.String()); err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (s *OperatorOptions) Complete() error {
	return nil
}

func (s OperatorOptions) Run(ctx context.Context) error {
	klog.Infof("Starting binary version %s+%s ...", v.Version.Version, v.Version.CommitHash)

	log.SetLogger(klogr.New()) // nolint:staticcheck

	cfg, err := clientcmd.BuildConfigFromFlags(s.MasterURL, s.KubeconfigPath)
	if err != nil {
		klog.Fatalf("Could not get Kubernetes config: %s", err)
	}

	cfg.QPS = float32(s.QPS)
	cfg.Burst = s.Burst

	crdClient, err := crd_cs.NewForConfig(cfg)
	if err != nil {
		setupLog.Error(err, "failed to create crd client")
		os.Exit(1)
	}
	err = controllers.EnsureCustomResourceDefinitions(crdClient)
	if err != nil {
		setupLog.Error(err, "failed to register crds")
		os.Exit(1)
	}

	mgr, err := manager.New(cfg, manager.Options{
		Scheme:                 apiserver.Scheme,
		Metrics:                metricsserver.Options{BindAddress: s.metricsAddr},
		HealthProbeBindAddress: s.probeAddr,
		LeaderElection:         s.enableLeaderElection,
		LeaderElectionID:       "f14948a0.grafana-operator.openviz.dev",
		NewClient:              cu.NewClient,
		Cache: cache.Options{
			SyncPeriod: &s.ResyncPeriod,
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &appcatalogapi.AppBinding{}, mona.DefaultGrafanaKey, func(rawObj client.Object) []string {
		app := rawObj.(*appcatalogapi.AppBinding)
		if v, ok := app.Annotations[mona.DefaultGrafanaKey]; ok && v == "true" {
			return []string{"true"}
		}
		return nil
	}); err != nil {
		klog.Error(err, "unable to set up AppBinding Indexer", "field", mona.DefaultGrafanaKey)
		os.Exit(1)
	}

	if err = (&openviz.GrafanaDashboardReconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		Recorder:             mgr.GetEventRecorderFor("grafana-dashboard-controller"),
		RequeueAfterDuration: s.RequeueAfterDuration,
	}).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create controller", "controller", "GrafanaDashboard")
		os.Exit(1)
	}
	if err = (&openviz.GrafanaDatasourceReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("grafana-datasource-controller"),
	}).SetupWithManager(mgr); err != nil {
		klog.Error(err, "unable to create controller", "controller", "GrafanaDatasource")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		klog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		klog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
	return nil
}
