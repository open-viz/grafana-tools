package openviz

import (
	"context"
	"time"

	"gomodules.xyz/pointer"
	core "k8s.io/api/core/v1"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	openvizapi "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	"gomodules.xyz/x/crypto/rand"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kmapi "kmodules.xyz/client-go/api/v1"
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
				},
			}
			Expect(k8sClient.Create(ctx, ab)).Should(Succeed())
		})
		It("Should update GrafanaDatasource Status to Current when new GrafanaDatasource are created", func() {
			By("By creating a new GrafanaDatasource")
			ctx := context.Background()
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

		})
	})
})
