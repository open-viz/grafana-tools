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

package e2e_test

import (
	"log"
	"testing"
	"time"

	"go.openviz.dev/grafana-tools/pkg/apiserver"
	"go.openviz.dev/grafana-tools/test/e2e/framework"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"
	"gomodules.xyz/logs"
	core "k8s.io/api/core/v1"
	"kmodules.xyz/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const (
	TIMEOUT = 20 * time.Minute
)

var root *framework.Framework

func TestE2e(t *testing.T) {
	logs.InitLogs()
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(TIMEOUT)

	reporterConfig := types.NewDefaultReporterConfig()
	reporterConfig.JUnitReport = "junit.xml"
	reporterConfig.JSONReport = "report.json"
	reporterConfig.Verbose = true
	RunSpecs(t, "e2e Suite", Label("kubeDB"), reporterConfig)
}

var _ = BeforeSuite(func() {
	By("Using kubeconfig from " + options.kubeConfig)
	clientConfig, err := clientcmd.BuildConfigFromContext(options.kubeConfig, options.kubeContext)
	Expect(err).NotTo(HaveOccurred())
	// raise throttling time. ref: https://github.com/appscode/voyager/issues/640
	clientConfig.Burst = 100
	clientConfig.QPS = 100

	mgr, err := manager.New(clientConfig, manager.Options{
		Scheme:                 apiserver.Scheme,
		Metrics:                metricsserver.Options{BindAddress: ""},
		HealthProbeBindAddress: "",
		LeaderElection:         false,
		LeaderElectionID:       "5b87adeb.grafana.test.openviz.dev",
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{
					&core.Namespace{},
					&core.Secret{},
					&core.Pod{},
				},
			},
		},
	})
	Expect(err).NotTo(HaveOccurred())
	// Framework
	root = framework.New(clientConfig, mgr.GetClient())

	go func() {
		if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
			log.Printf("error from manager: %v", err)
		}
	}()

	By("Creating namespace " + root.Namespace())
	err = root.CreateNamespace()
	Expect(err).NotTo(HaveOccurred())

	By("Creating grafana server")
	err = root.DeployGrafanaServer()
	Expect(err).NotTo(HaveOccurred())

	By("Waiting for grafana server to be ready")
	root.WaitForGrafanaServerToBeReady()
	root.EventuallyCRD().Should(Succeed())
})

var _ = AfterSuite(func() {
	By("Deleting grafana server")
	err := root.DeleteGrafanaServer()
	Expect(err).NotTo(HaveOccurred())

	By("Deleting Namespace")
	err = root.DeleteNamespace()
	Expect(err).NotTo(HaveOccurred())
})
