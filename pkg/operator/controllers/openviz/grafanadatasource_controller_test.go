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

package openviz

import (
	"context"
	"time"

	openvizapi "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"gomodules.xyz/pointer"
	"gomodules.xyz/x/crypto/rand"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kmapi "kmodules.xyz/client-go/api/v1"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
)

var _ = Describe("GrafanaDatasource Controller", func() {
	const (
		GrafanaDatasourceName = "test-ds"
		CommonNS              = "default"
		SecretName            = "grafana-auth-ds"
		AppBindingName        = "grafana-ab-ds"
		GrafanaAPIKey         = "admin:prom-operator"

		timeout  = time.Minute
		duration = time.Minute
		interval = time.Second
	)

	Context("When updating GrafanaDatasource Status to Current", func() {
		It("Should create GrafanaDatasource resource and check status to Current", func() {
			ctx := context.Background()
			By("Creating Grafana Secret")
			auth := &core.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      SecretName,
					Namespace: CommonNS,
				},
				StringData: map[string]string{
					"apiKey": GrafanaAPIKey,
				},
				Type: core.SecretTypeOpaque,
			}
			Expect(k8sClient.Create(ctx, auth)).Should(Succeed())

			By("Creating Grafana AppBinding")
			ab := &appcatalog.AppBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      AppBindingName,
					Namespace: CommonNS,
				},
				Spec: appcatalog.AppBindingSpec{
					ClientConfig: appcatalog.ClientConfig{
						URL: pointer.StringP("http://localhost:3000/"),
					},
					Secret: &core.LocalObjectReference{
						Name: SecretName,
					},
				},
			}
			Expect(k8sClient.Create(ctx, ab)).Should(Succeed())

			By("By creating a new GrafanaDatasource")
			ds := &openvizapi.GrafanaDatasource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      GrafanaDatasourceName,
					Namespace: CommonNS,
				},
				Spec: openvizapi.GrafanaDatasourceSpec{
					GrafanaRef: &kmapi.ObjectReference{
						Namespace: CommonNS,
						Name:      AppBindingName,
					},
					OrgID:     1,
					Name:      rand.WithUniqSuffix("prom-ds"),
					Type:      openvizapi.GrafanaDatasourceTypePrometheus,
					Access:    openvizapi.GrafanaDatasourceAccessTypeProxy,
					URL:       "https://just.a.dummy.url/",
					IsDefault: false,
					Editable:  false,
				},
			}
			Expect(k8sClient.Create(ctx, ds)).Should(Succeed())

			dsKey := types.NamespacedName{Namespace: CommonNS, Name: GrafanaDatasourceName}
			createdDS := &openvizapi.GrafanaDatasource{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, dsKey, createdDS)
				if err != nil {
					return false
				}
				return createdDS.Status.Phase == openvizapi.GrafanaPhaseCurrent
			}, timeout, interval).Should(BeTrue())

			By("Deleting the GrafanaDatasource resource")
			Expect(k8sClient.Delete(ctx, createdDS)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, dsKey, createdDS)

				return apierrors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		})
	})
})
