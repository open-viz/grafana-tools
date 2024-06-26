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

package framework

import (
	"context"
	"time"

	sdk "go.openviz.dev/grafana-sdk"
	"go.openviz.dev/grafana-tools/pkg/grafana"

	"github.com/go-resty/resty/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	gSvrInterval = time.Millisecond * 250
	gSvrTimeout  = time.Minute * 20
)

func (f *Framework) GetGrafanaClient() (*sdk.Client, error) {
	ab := &appcatalog.AppBinding{}
	if err := f.cc.Get(context.TODO(), client.ObjectKey{Namespace: f.namespace, Name: f.name}, ab); err != nil {
		return nil, err
	}

	auth := &core.Secret{}
	if err := f.cc.Get(context.TODO(), client.ObjectKey{Namespace: f.namespace, Name: ab.Spec.Secret.Name}, auth); err != nil {
		return nil, err
	}

	cfg, err := grafana.GetGrafanaConfig(ab, auth)
	if err != nil {
		return nil, err
	}
	httpClient := resty.New()
	if cfg.TLS != nil && len(cfg.TLS.CABundle) > 0 {
		httpClient.SetRootCertificateFromString(string(cfg.TLS.CABundle))
	}

	return sdk.NewClient(cfg.Addr, cfg.AuthConfig, httpClient)
}

func (f *Framework) DeployGrafanaServer() error {
	By("Creating grafana service")
	if err := f.CreateGrafanaService(); err != nil {
		return err
	}

	By("Creating grafana deployment")
	if err := f.CreateGrafanaDeployment(); err != nil {
		return err
	}

	return nil
}

func (f *Framework) DeleteGrafanaServer() error {
	By("Deleting grafana deployment")
	if err := f.DeleteGrafanaDeployment(); err != nil {
		return err
	}

	By("Deleting grafana service")
	if err := f.DeleteGrafanaService(); err != nil {
		return err
	}

	return nil
}

func (f *Framework) WaitForGrafanaServerToBeReady() {
	Eventually(func() bool {
		dpl := &apps.Deployment{}
		err := f.cc.Get(context.TODO(), client.ObjectKey{Namespace: f.namespace, Name: f.name}, dpl)
		Expect(err).NotTo(HaveOccurred())

		pods := &core.PodList{}
		opts := &client.ListOptions{
			Namespace: f.namespace,
		}
		selector := client.MatchingLabels(dpl.Labels)
		selector.ApplyToList(opts)
		err = f.cc.List(context.TODO(), pods, opts)
		Expect(err).NotTo(HaveOccurred())

		for _, pod := range pods.Items {
			if pod.Status.Phase != core.PodRunning {
				return false
			}
		}
		return true
	}, gSvrTimeout, gSvrInterval).Should(BeTrue())

	Eventually(func() bool {
		svc := &core.Service{}
		err := f.cc.Get(context.TODO(), client.ObjectKey{Namespace: f.namespace, Name: f.name}, svc)
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())

		return err == nil
	}, gSvrTimeout, gSvrInterval)

	Eventually(func() bool {
		return f.isGrafanaReady()
	}, gSvrTimeout, gSvrInterval)
}
