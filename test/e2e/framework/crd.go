/*
Copyright The Kubeform Authors.

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
	"errors"
	"time"

	. "github.com/onsi/gomega"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (f *Framework) EventuallyCRD() GomegaAsyncAssertion {
	return Eventually(
		func() error {
			// Check ServiceAccount CRD
			if _, err := f.client.GrafanaV1alpha1().Datasources(core.NamespaceAll).List(metav1.ListOptions{}); err != nil {
				return errors.New("CRD Datasource is not ready")
			}

			// Check ResourceGroup CRD
			if _, err := f.client.GrafanaV1alpha1().Dashboards(core.NamespaceAll).List(metav1.ListOptions{}); err != nil {
				return errors.New("CRD Dashboard is not ready")
			}

			// Check DbInstance CRD
			if _, err := f.client.GrafanaV1alpha1().DashboardTemplates(core.NamespaceAll).List(metav1.ListOptions{}); err != nil {
				return errors.New("CRD DashboardTemplate is not ready")
			}
			return nil
		},
		time.Minute*2,
		time.Second*10,
	)
}
