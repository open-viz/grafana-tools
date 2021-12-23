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
	"context"
	"io/ioutil"
	"os"
	"time"

	api "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/test/e2e/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"gomodules.xyz/pointer"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamic_util "kmodules.xyz/client-go/dynamic"
	appcat "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
)

const (
	retryTimeout = 1 * time.Minute
)

var _ = Describe("GrafanaRef Operator E2E testing", func() {
	var (
		f *framework.Invocation
	)

	var (
		getModelFromGrafanaDashboardJson = func(filename string) []byte {
			By("Selecting the grafanadashboard model")
			file, err := os.Open(filename)
			Expect(err).NotTo(HaveOccurred())

			modelData, err := ioutil.ReadAll(file)
			Expect(err).NotTo(HaveOccurred())

			return modelData
		}

		createAppBindingAndSecret = func() {
			By("Creating AppBinding " + f.AppBindingName())
			err := f.CreateAppBinding()
			Expect(err).NotTo(HaveOccurred())

			By("Creating Secret " + f.SecretName())
			err = f.CreateSecret(options.apiKey)
			Expect(err).NotTo(HaveOccurred())
		}

		waitForGrafanaDashboardToGetToFinalPhase = func() {
			By("Waiting for the grafanadashboard to get to final phase")
			Eventually(func() bool {
				grafanadashboard, err := f.GetGrafanaDashboard()
				Expect(err).NotTo(HaveOccurred())

				return grafanadashboard.Status.Phase == api.GrafanaPhaseSuccess || grafanadashboard.Status.Phase == api.GrafanaPhaseFailed
			}, retryTimeout, 100*time.Millisecond).Should(BeTrue())

		}

		waitForGrafanaDashboardToBeTerminated = func(grafanadashboard *api.GrafanaDashboard) {
			By("Deleting grafanadashboard " + grafanadashboard.Name)
			err := f.DeleteGrafanaDashboard()
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for the grafanadashboard to be terminated")
			Eventually(func() bool {
				_, err := f.GetGrafanaDashboard()
				return kerr.IsNotFound(err)
			}, retryTimeout, 100*time.Millisecond).Should(BeTrue())
		}
	)

	BeforeEach(func() {
		f = root.Invoke()
	})

	Describe("GrafanaDashboard Operation", func() {
		var (
			grafanadashboard *api.GrafanaDashboard
		)

		BeforeEach(func() {
			grafanadashboard = &api.GrafanaDashboard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      f.Name(),
					Namespace: f.Namespace(),
				},
				Spec: api.GrafanaDashboardSpec{
					GrafanaRef: &api.TargetRef{
						Name: f.AppBindingName(),
					},
					Overwrite: true,
				},
			}

		})

		Context("Successful creation of a grafanadashboard resource", func() {
			BeforeEach(func() {
				model := getModelFromGrafanaDashboardJson("dashboard-with-panels-with-mixed-yaxes.json")
				grafanadashboard.Spec.Model = &runtime.RawExtension{
					Raw: model,
				}

				createAppBindingAndSecret()
			})

			It("should insert a grafanadashboard into grafana database", func() {
				err := f.CreateGrafanaDashboard(grafanadashboard)
				Expect(err).NotTo(HaveOccurred())

			})

			AfterEach(func() {
				By("Verifying grafanadashboard has been succeeded")
				grafanadashboard, err := f.GetGrafanaDashboard()
				Expect(err).NotTo(HaveOccurred())

				Expect(grafanadashboard.Status.Phase).To(BeEquivalentTo(api.GrafanaPhaseSuccess))

				Expect(grafanadashboard.Status.Dashboard.ID).NotTo(BeNil())
				Expect(grafanadashboard.Status.Dashboard.UID).NotTo(BeNil())
				Expect(grafanadashboard.Status.Dashboard.Version).To(BeEquivalentTo(pointer.Int64P(grafanadashboard.Status.ObservedGeneration)))

			})
		})

		Context("Unsuccessful creation of a grafanadashboard resource", func() {
			BeforeEach(func() {
				model := getModelFromGrafanaDashboardJson("dashboard-with-panels-with-mixed-yaxes.json")
				grafanadashboard.Spec.Model = &runtime.RawExtension{
					Raw: model,
				}

				options.apiKey = "admin:not-password"
				createAppBindingAndSecret()
			})

			It("should not insert a grafanadashboard into grafana database", func() {
				err := f.CreateGrafanaDashboard(grafanadashboard)
				Expect(err).NotTo(HaveOccurred())

			})

			AfterEach(func() {
				grafanadashboard, err := f.GetGrafanaDashboard()
				Expect(err).NotTo(HaveOccurred())
				Expect(grafanadashboard.Status.Phase).To(BeEquivalentTo(api.GrafanaPhaseFailed))
				Expect(grafanadashboard.Status.Dashboard).To(BeNil())
			})
		})

		Context("Unsuccessful creation of a grafanadashboard resource", func() {
			BeforeEach(func() {
				By("Not providing model data")
				createAppBindingAndSecret()
			})

			It("should not insert a grafanadashboard into grafana database", func() {
				err := f.CreateGrafanaDashboard(grafanadashboard)
				Expect(err).NotTo(HaveOccurred())

			})

			AfterEach(func() {
				grafanadashboard, err := f.GetGrafanaDashboard()
				Expect(err).NotTo(HaveOccurred())
				Expect(grafanadashboard.Status.Phase).To(BeEquivalentTo(api.GrafanaPhaseFailed))
				Expect(grafanadashboard.Status.Dashboard).To(BeNil())
			})
		})

		JustAfterEach(func() {
			waitForGrafanaDashboardToGetToFinalPhase()
		})

		AfterEach(func() {
			waitForGrafanaDashboardToBeTerminated(grafanadashboard)

			By("Deleting Secret")
			err := root.DeleteSecret()
			Expect(err).NotTo(HaveOccurred())

			By("Deleting AppBinding")
			err = root.DeleteAppBinding()
			Expect(err).NotTo(HaveOccurred())

			By("Wait until AppBinding is deleted")
			ctx := context.TODO()
			ctx, cancel := context.WithTimeout(ctx, retryTimeout)
			defer cancel()
			ri := dynamic.NewForConfigOrDie(f.RestConfig()).
				Resource(appcat.SchemeGroupVersion.WithResource(appcat.ResourceApps)).
				Namespace(f.Namespace())
			err = dynamic_util.WaitUntilDeleted(ri, ctx.Done(), f.AppBindingName())
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
