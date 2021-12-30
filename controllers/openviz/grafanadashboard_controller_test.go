package openviz

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	openvizapi "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	"gomodules.xyz/pointer"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kmapi "kmodules.xyz/client-go/api/v1"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
)

var _ = Describe("GrafanaDashboard Controller", func() {
	const (
		GrafanaDashboardName = "test-db"
		CommonNS             = "default"
		SecretName           = "grafana-auth-db"
		AppBindingName       = "grafana-ab-db"
		GrafanaAPIKey        = "admin:prom-operator"

		timeout  = time.Minute
		duration = time.Minute
		interval = time.Second
	)

	Context("When updating GrafanaDashboard Status to Current", func() {
		It("Should create the necessary auth", func() {
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
			dsConfig := &openvizapi.GrafanaConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: openvizapi.GroupVersion.String(),
					Kind:       openvizapi.ResourceKindGrafanaConfiguration,
				},
				Datasource: "prometheus-ds",
			}
			dsConfigByte, err := json.Marshal(dsConfig)
			Expect(err).NotTo(HaveOccurred())
			ab := &appcatalog.AppBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      AppBindingName,
					Namespace: CommonNS,
				},
				Spec: appcatalog.AppBindingSpec{
					ClientConfig: appcatalog.ClientConfig{
						URL: pointer.StringP("http://localhost:3001/"),
					},
					Secret: &core.LocalObjectReference{
						Name: SecretName,
					},
					Parameters: &runtime.RawExtension{Raw: dsConfigByte},
				},
			}
			Expect(k8sClient.Create(ctx, ab)).Should(Succeed())
		})
		It("Should update GrafanaDashboard Status to Current when new GrafanaDashboard are created", func() {
			By("By creating a new GrafanaDashboard")
			ctx := context.Background()
			model, err := ioutil.ReadFile("../../testdata/dashboard_model.json")
			Expect(err).NotTo(HaveOccurred())
			db := &openvizapi.GrafanaDashboard{
				ObjectMeta: metav1.ObjectMeta{
					Name:      GrafanaDashboardName,
					Namespace: CommonNS,
				},
				Spec: openvizapi.GrafanaDashboardSpec{
					GrafanaRef: &kmapi.ObjectReference{
						Namespace: CommonNS,
						Name:      AppBindingName,
					},
					FolderID:  pointer.Int64P(0),
					Model:     &runtime.RawExtension{Raw: model},
					Overwrite: true,
					Templatize: &openvizapi.ModelTemplateConfiguration{
						Datasource: true,
					},
				},
			}
			Expect(k8sClient.Create(ctx, db)).Should(Succeed())

			dbKey := types.NamespacedName{Namespace: CommonNS, Name: GrafanaDashboardName}
			createdDB := &openvizapi.GrafanaDashboard{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, dbKey, createdDB)
				if err != nil {
					return false
				}
				return createdDB.Status.Phase == openvizapi.GrafanaPhaseCurrent
			}, timeout, interval).Should(BeTrue())

		})
	})
})
