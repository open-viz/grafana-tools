/*
Copyright The Searchlight Authors.

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

	"github.com/grafana-tools/sdk"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func (f *Framework) GetGrafanaClient() (*sdk.Client, error) {
	appBinding, err := f.appcatClient.AppBindings(f.namespace).Get(context.TODO(), f.name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	secret, err := f.kubeClient.CoreV1().Secrets(f.namespace).Get(context.TODO(), appBinding.Spec.Secret.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	apiURL, apiKey, err := f.getApiURLandApiKey(appBinding, secret)
	if err != nil {
		return nil, err
	}
	return sdk.NewClient(apiURL, apiKey, sdk.DefaultHTTPClient), nil
}

func (f *Framework) DeployGrafanaServer() error {
	By("Creating grafana deployment")
	if err := f.CreateGrafanaDeployment(); err != nil {
		return err
	}

	By("Creating grafana service")
	if err := f.CreateGrafanaService(); err != nil {
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
		deploy, err := f.kubeClient.AppsV1().Deployments(f.namespace).Get(context.TODO(), f.name, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		pods, err := f.kubeClient.CoreV1().Pods(deploy.Namespace).List(context.TODO(), metav1.ListOptions{
			LabelSelector: labels.Set(deploy.GetLabels()).String(),
		})
		Expect(err).NotTo(HaveOccurred())

		for _, pod := range pods.Items {
			if pod.Status.Phase != core.PodRunning {
				return false
			}
		}
		return true
	}, 5*time.Minute, 100*time.Millisecond).Should(BeTrue())
}
