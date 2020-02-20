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

package controller

import (
	"fmt"

	api "go.searchlight.dev/grafana-operator/apis/grafana/v1alpha1"
	cs "go.searchlight.dev/grafana-operator/client/clientset/versioned"
	"go.searchlight.dev/grafana-operator/client/clientset/versioned/typed/grafana/v1alpha1/util"
	grafanainformers "go.searchlight.dev/grafana-operator/client/informers/externalversions"
	grafana_listers "go.searchlight.dev/grafana-operator/client/listers/grafana/v1alpha1"
	"go.searchlight.dev/grafana-operator/pkg/eventer"

	pcm "github.com/coreos/prometheus-operator/pkg/client/versioned/typed/monitoring/v1"
	"github.com/golang/glog"
	"github.com/grafana-tools/sdk"
	core "k8s.io/api/core/v1"
	crd_api "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	crd_cs "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	reg_util "kmodules.xyz/client-go/admissionregistration/v1beta1"
	crdutils "kmodules.xyz/client-go/apiextensions/v1beta1"
	"kmodules.xyz/client-go/tools/queue"
	appcat "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	appcat_cs "kmodules.xyz/custom-resources/client/clientset/versioned/typed/appcatalog/v1alpha1"
)

type GrafanaController struct {
	config
	clientConfig *rest.Config

	kubeClient       kubernetes.Interface
	extClient        cs.Interface
	appCatalogClient appcat_cs.AppcatalogV1alpha1Interface
	crdClient        crd_cs.ApiextensionsV1beta1Interface
	recorder         record.EventRecorder
	// Prometheus client
	promClient pcm.MonitoringV1Interface

	kubeInformerFactory informers.SharedInformerFactory
	extInformerFactory  grafanainformers.SharedInformerFactory

	// for Dashboard
	dashboardQueue    *queue.Worker
	dashboardInformer cache.SharedIndexInformer
	dashboardLister   grafana_listers.DashboardLister

	// Grafana client
	grafanaClient *sdk.Client
}

func (c *GrafanaController) ensureCustomResourceDefinitions() error {
	crds := []*crd_api.CustomResourceDefinition{
		api.Dashboard{}.CustomResourceDefinition(),
		appcat.AppBinding{}.CustomResourceDefinition(),
	}
	return crdutils.RegisterCRDs(c.kubeClient.Discovery(), c.crdClient, crds)
}

func (c *GrafanaController) Run(stopCh <-chan struct{}) {
	go c.RunInformers(stopCh)

	if c.EnableMutatingWebhook {
		cancel, _ := reg_util.SyncMutatingWebhookCABundle(c.clientConfig, mutatingWebhook)
		defer cancel()
	}
	if c.EnableValidatingWebhook {
		cancel, _ := reg_util.SyncValidatingWebhookCABundle(c.clientConfig, validatingWebhook)
		defer cancel()
	}

	<-stopCh
}

func (c *GrafanaController) RunInformers(stopCh <-chan struct{}) {
	defer runtime.HandleCrash()

	glog.Info("Starting Grafana controller")

	c.extInformerFactory.Start(stopCh)
	for _, v := range c.extInformerFactory.WaitForCacheSync(stopCh) {
		if !v {
			runtime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
			return
		}
	}

	//For Dashboard
	go c.dashboardQueue.Run(stopCh)

	<-stopCh
	glog.Info("Stopping Vault operator")
}

func (c *GrafanaController) pushFailureEvent(dashboard *api.Dashboard, reason string) {
	c.recorder.Eventf(
		dashboard,
		core.EventTypeWarning,
		eventer.EventReasonFailedToStart,
		`Failed to complete operation for Dashboard: "%v". Reason: %v`,
		dashboard.Name,
		reason,
	)
	dashboard, err := util.UpdateDashboardStatus(c.extClient.GrafanaV1alpha1(), dashboard, func(in *api.DashboardStatus) *api.DashboardStatus {
		in.Phase = api.DashboardPhaseFailed
		in.Reason = reason
		in.Conditions = append(in.Conditions, api.DashboardCondition{
			Type:    api.DashboardConditionFailure,
			Status:  core.ConditionTrue,
			Reason:  reason,
			Message: reason,
		})
		in.ObservedGeneration = dashboard.Generation
		return in
	})
	if err != nil {
		c.recorder.Eventf(
			dashboard,
			core.EventTypeWarning,
			eventer.EventReasonFailedToUpdate,
			err.Error(),
		)
	}
}
