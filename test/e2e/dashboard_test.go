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
	"io"
	"os"
	"time"

	api "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/test/e2e/framework"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	kmapi "kmodules.xyz/client-go/api/v1"
	dynamic_util "kmodules.xyz/client-go/dynamic"
	appcat "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
)

const (
	timeout  = 5 * time.Minute
	interval = 250 * time.Millisecond
)

const (
	grafanaDashboardJsonFile        = "dashboard-with-panels-with-mixed-yaxes.json"
	updatedGrafanaDashboardJsonFile = "updated-dashboard-with-panels.json"
	invalidGrafanaDashboardJsonFile = "invalid-dashboard.json"
)

var _ = Describe("GrafanaRef Operator E2E testing", func() {
	var f *framework.Invocation

	var (
		getModelFromGrafanaDashboardJson = func(filename string) []byte {
			By("Selecting the grafanadashboard model")
			file, err := os.Open(filename)
			Expect(err).NotTo(HaveOccurred())

			modelData, err := io.ReadAll(file)
			Expect(err).NotTo(HaveOccurred())

			return modelData
		}

		getInitialGrafanaDashboardResource = func(jsonFilePath string) *api.GrafanaDashboard {
			model := getModelFromGrafanaDashboardJson(jsonFilePath)
			grafanaDashboard := &api.GrafanaDashboard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      f.Name(),
					Namespace: f.Namespace(),
				},
				Spec: api.GrafanaDashboardSpec{
					GrafanaRef: &kmapi.ObjectReference{
						Name:      f.AppBindingName(),
						Namespace: f.AppBindingNamespace(),
					},
					Model:     &runtime.RawExtension{Raw: model},
					Overwrite: true,
				},
			}
			return grafanaDashboard
		}

		createAppBinding = func() {
			By("Creating AppBinding " + f.AppBindingName())
			Expect(f.CreateAppBinding()).Should(Succeed())
		}

		createSecret = func(username, password string) {
			By("Creating Secret " + f.SecretName())
			Expect(f.CreateSecret(username, password)).Should(Succeed())
		}

		createDefaultAppBinding = func() {
			By("Creating Default AppBinding " + f.AppBindingName())
			Expect(f.CreateDefaultAppBinding()).Should(Succeed())
		}

		waitForGrafanaDashboardToGetToFinalPhase = func() {
			Eventually(func() bool {
				grafanadashboard, err := f.GetGrafanaDashboard()
				if err != nil {
					return false
				}

				return grafanadashboard.Status.Phase == api.GrafanaPhaseCurrent || grafanadashboard.Status.Phase == api.GrafanaPhaseFailed
			}, timeout, interval).Should(BeTrue())
		}

		waitForGrafanaDashboardObservedGenerationToBeUpdated = func() {
			Eventually(func() bool {
				grafanadashboard, err := f.GetGrafanaDashboard()
				if err != nil {
					return false
				}

				return grafanadashboard.Status.ObservedGeneration == grafanadashboard.Generation
			}, timeout, interval).Should(BeTrue())
		}

		waitForGrafanaDashboardToBeTerminated = func(gdb *api.GrafanaDashboard) {
			By("Deleting grafanadashboard " + gdb.Name)
			err := f.DeleteGrafanaDashboard(gdb)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				_, err := f.GetGrafanaDashboard()
				return kerr.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		}
	)

	BeforeEach(func() {
		f = root.Invoke()
	})

	Describe("GrafanaDashboard Operation", func() {
		Context("Successful creation of a GrafanaDashboard resource with given AppBinding", func() {
			It("should insert a grafanaDashboard into grafana database", func() {
				By("Crating Secret with Grafana auth")
				createSecret(options.grafanaUsername, options.grafanaPassword)

				By("Creating AppBinding")
				createAppBinding()

				By("Creating Dashboard")
				gDashboard := getInitialGrafanaDashboardResource(grafanaDashboardJsonFile)
				err := f.CreateOrUpdateGrafanaDashboard(gDashboard)
				Expect(err).NotTo(HaveOccurred())

				By("Wait for GrafanaDashboardPhase to get Final phase")
				waitForGrafanaDashboardToGetToFinalPhase()

				By("Verifying GrafanaDashboard has been succeeded")
				err = f.WaitForGrafanaPhaseToBeCurrent()
				Expect(err).NotTo(HaveOccurred())

				By("Deleting grafanaDashboard")
				waitForGrafanaDashboardToBeTerminated(gDashboard)
			})

			It("should insert a grafana dashboard into the grafana database and update the dashboard", func() {
				By("Crating Secret with Grafana auth")
				createSecret(options.grafanaUsername, options.grafanaPassword)

				By("Creating AppBinding")
				createAppBinding()

				By("Creating Dashboard")
				gDashboard := getInitialGrafanaDashboardResource(grafanaDashboardJsonFile)
				err := f.CreateOrUpdateGrafanaDashboard(gDashboard)
				Expect(err).NotTo(HaveOccurred())

				By("Wait for GrafanaDashboardPhase to get Final phase")
				waitForGrafanaDashboardToGetToFinalPhase()

				By("Verifying GrafanaDashboard has been succeeded")
				err = f.WaitForGrafanaPhaseToBeCurrent()
				Expect(err).NotTo(HaveOccurred())

				updatedModel := getModelFromGrafanaDashboardJson(updatedGrafanaDashboardJsonFile)
				gDashboard.Spec.Model.Raw = updatedModel

				By("Update the dashboard with updated json model")
				err = f.CreateOrUpdateGrafanaDashboard(gDashboard)
				Expect(err).NotTo(HaveOccurred())

				By("Deleting grafanaDashboard")
				waitForGrafanaDashboardToBeTerminated(gDashboard)
			})
		})

		Context("Successful creation of a GrafanaDashboard resource with default grafana AppBinding", func() {
			It("should insert a grafanaDashboard into grafana database with default grafana AppBinding", func() {
				By("Crating Secret with Grafana auth")
				createSecret(options.grafanaUsername, options.grafanaPassword)

				By("Creating Default AppBinding")
				createDefaultAppBinding()

				By("Creating Dashboard")
				gDashboard := getInitialGrafanaDashboardResource(grafanaDashboardJsonFile)
				gDashboard.Spec.GrafanaRef = nil
				err := f.CreateOrUpdateGrafanaDashboard(gDashboard)
				Expect(err).NotTo(HaveOccurred())

				By("Wait for GrafanaDashboardPhase to get Final phase")
				waitForGrafanaDashboardToGetToFinalPhase()

				By("Verifying GrafanaDashboard has been succeeded")
				err = f.WaitForGrafanaPhaseToBeCurrent()
				Expect(err).NotTo(HaveOccurred())

				By("Deleting grafanaDashboard")
				waitForGrafanaDashboardToBeTerminated(gDashboard)
			})

			It("should insert a grafana dashboard into the grafana database and update the dashboard with default grafana AppBinding", func() {
				By("Crating Secret with Grafana auth")
				createSecret(options.grafanaUsername, options.grafanaPassword)

				By("Creating Default Grafana AppBinding")
				createDefaultAppBinding()

				By("Creating Dashboard")
				gDashboard := getInitialGrafanaDashboardResource(grafanaDashboardJsonFile)
				gDashboard.Spec.GrafanaRef = nil
				err := f.CreateOrUpdateGrafanaDashboard(gDashboard)
				Expect(err).NotTo(HaveOccurred())

				By("Wait for GrafanaDashboardPhase to get Final phase")
				waitForGrafanaDashboardToGetToFinalPhase()

				By("Verifying GrafanaDashboard has been succeeded")
				err = f.WaitForGrafanaPhaseToBeCurrent()
				Expect(err).NotTo(HaveOccurred())

				updatedModel := getModelFromGrafanaDashboardJson(updatedGrafanaDashboardJsonFile)
				gDashboard.Spec.Model.Raw = updatedModel

				By("Update the dashboard with updated json model")
				err = f.CreateOrUpdateGrafanaDashboard(gDashboard)
				Expect(err).NotTo(HaveOccurred())

				By("Deleting grafanaDashboard")
				waitForGrafanaDashboardToBeTerminated(gDashboard)
			})
		})

		Context("Unsuccessful creation of a grafanaDashboard resource", func() {
			It("should not insert a grafanaDashboard into grafana database with invalid grafana auth", func() {
				By("Crating Secret with invalid Grafana auth")
				createSecret("admin", "wrong-password")

				By("Creating Grafana AppBinding")
				createAppBinding()

				By("Creating Dashboard")
				gDashboard := getInitialGrafanaDashboardResource(grafanaDashboardJsonFile)
				err := f.CreateOrUpdateGrafanaDashboard(gDashboard)
				Expect(err).NotTo(HaveOccurred())

				By("Wait for GrafanaDashboardPhase to get Final phase")
				waitForGrafanaDashboardToGetToFinalPhase()

				By("Checking GrafanaDashboard failed status")
				gdb, err := f.GetGrafanaDashboard()
				Expect(err).NotTo(HaveOccurred())
				Expect(gdb.Status.Phase).To(Equal(api.GrafanaPhaseFailed))
				Expect(gdb.Status.Dashboard).To(BeNil())

				By("Deleting grafanaDashboard")
				waitForGrafanaDashboardToBeTerminated(gDashboard)
			})

			It("should not insert a grafanaDashboard into grafana database with no model data", func() {
				By("Crating Secret with Grafana auth")
				createSecret(options.grafanaUsername, options.grafanaPassword)

				By("Creating Grafana AppBinding")
				createAppBinding()

				By("Creating Dashboard")
				gDashboard := getInitialGrafanaDashboardResource(grafanaDashboardJsonFile)
				gDashboard.Spec.Model = nil
				err := f.CreateOrUpdateGrafanaDashboard(gDashboard)
				Expect(err).NotTo(HaveOccurred())

				By("Wait for GrafanaDashboardPhase to get Final phase")
				waitForGrafanaDashboardToGetToFinalPhase()

				By("Checking GrafanaDashboard failed status")
				gdb, err := f.GetGrafanaDashboard()
				Expect(err).NotTo(HaveOccurred())
				Expect(gdb.Status.Phase).To(BeEquivalentTo(api.GrafanaPhaseFailed))
				Expect(gdb.Status.Dashboard).To(BeNil())

				By("Deleting grafanaDashboard")
				waitForGrafanaDashboardToBeTerminated(gDashboard)
			})

			It("should not insert a grafanaDashboard into grafana database with invalid dashboard json data", func() {
				By("Crating Secret with Grafana auth")
				createSecret(options.grafanaUsername, options.grafanaPassword)

				By("Creating Grafana AppBinding")
				createAppBinding()

				By("Creating Dashboard")
				gDashboard := getInitialGrafanaDashboardResource(invalidGrafanaDashboardJsonFile)
				err := f.CreateOrUpdateGrafanaDashboard(gDashboard)
				Expect(err).NotTo(HaveOccurred())

				By("Wait for GrafanaDashboardPhase to get Final phase")
				waitForGrafanaDashboardToGetToFinalPhase()

				By("Wait for GrafanaDashboard ObservedGeneration to be updated")
				waitForGrafanaDashboardObservedGenerationToBeUpdated()

				By("Checking GrafanaDashboard failed status with updated observed generation")
				gdb, err := f.GetGrafanaDashboard()
				Expect(err).NotTo(HaveOccurred())
				Expect(gdb.Status.Phase).To(BeEquivalentTo(api.GrafanaPhaseFailed))
				Expect(gdb.Status.Dashboard).To(BeNil())
				// As the given dashboard json is invalid, observer generation should be updated by the controller
				Expect(gdb.Status.ObservedGeneration).To(Equal(gdb.Generation))

				By("Deleting grafanaDashboard")
				waitForGrafanaDashboardToBeTerminated(gDashboard)
			})
		})

		AfterEach(func() {
			By("Deleting Secret")
			err := root.DeleteSecret()
			Expect(err).NotTo(HaveOccurred())

			By("Deleting AppBinding")
			err = root.DeleteAppBinding()
			Expect(err).NotTo(HaveOccurred())

			By("Wait until AppBinding is deleted")
			ctx := context.TODO()
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			ri := dynamic.NewForConfigOrDie(f.RestConfig()).
				Resource(appcat.SchemeGroupVersion.WithResource(appcat.ResourceApps)).
				Namespace(f.Namespace())
			err = dynamic_util.WaitUntilDeleted(ri, ctx.Done(), f.AppBindingName())
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
