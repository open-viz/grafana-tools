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

package controller

import (
	"context"
	"fmt"

	api "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	cs "go.openviz.dev/grafana-tools/client/clientset/versioned"
	"go.openviz.dev/grafana-tools/client/clientset/versioned/typed/openviz/v1alpha1/util"
	grafanainformers "go.openviz.dev/grafana-tools/client/informers/externalversions"
	grafana_listers "go.openviz.dev/grafana-tools/client/listers/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/pkg/operator/eventer"

	"github.com/grafana-tools/sdk"
	pcm "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned/typed/monitoring/v1"
	core "k8s.io/api/core/v1"
	crd_cs "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	reg_util "kmodules.xyz/client-go/admissionregistration/v1"
	kmapi "kmodules.xyz/client-go/api/v1"
	"kmodules.xyz/client-go/apiextensions"
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
	crdClient        crd_cs.Interface
	recorder         record.EventRecorder
	// Prometheus client
	promClient pcm.MonitoringV1Interface

	kubeInformerFactory informers.SharedInformerFactory
	extInformerFactory  grafanainformers.SharedInformerFactory

	// for Dashboard
	dashboardQueue    *queue.Worker
	dashboardInformer cache.SharedIndexInformer
	dashboardLister   grafana_listers.DashboardLister

	//for DataSource
	datasourceQueue    *queue.Worker
	datasourceInformer cache.SharedIndexInformer
	datasourceLister   grafana_listers.DatasourceLister

	// Grafana client
	grafanaClient *sdk.Client
}

func (c *GrafanaController) ensureCustomResourceDefinitions() error {
	crds := []*apiextensions.CustomResourceDefinition{
		api.Dashboard{}.CustomResourceDefinition(),
		api.Datasource{}.CustomResourceDefinition(),
		appcat.AppBinding{}.CustomResourceDefinition(),
	}
	return apiextensions.RegisterCRDs(c.crdClient, crds)
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

	klog.Info("Starting Grafana controller")

	c.extInformerFactory.Start(stopCh)
	for _, v := range c.extInformerFactory.WaitForCacheSync(stopCh) {
		if !v {
			runtime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
			return
		}
	}

	//For Dashboard
	go c.dashboardQueue.Run(stopCh)

	//For Datasource
	go c.datasourceQueue.Run(stopCh)

	<-stopCh
	klog.Info("Stopping Vault operator")
}

func (c *GrafanaController) pushDashboardFailureEvent(dashboard *api.Dashboard, reason string) {
	c.recorder.Eventf(
		dashboard,
		core.EventTypeWarning,
		eventer.EventReasonFailedToStart,
		`Failed to complete operation for Dashboard: "%v". Reason: %v`,
		dashboard.Name,
		reason,
	)
	dashboard, err := util.UpdateDashboardStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), dashboard.ObjectMeta, func(in *api.DashboardStatus) *api.DashboardStatus {
		in.Phase = api.GrafanaPhaseFailed
		in.Reason = reason
		in.Conditions = kmapi.SetCondition(in.Conditions, kmapi.Condition{
			Type:    kmapi.ConditionFailed,
			Status:  core.ConditionTrue,
			Reason:  reason,
			Message: reason,
		})
		in.ObservedGeneration = dashboard.Generation
		return in
	}, metav1.UpdateOptions{})
	if err != nil {
		c.recorder.Eventf(
			dashboard,
			core.EventTypeWarning,
			eventer.EventReasonFailedToUpdate,
			err.Error(),
		)
	}
}

func (c *GrafanaController) pushDatasourceFailureEvent(ds *api.Datasource, reason string) {
	c.recorder.Eventf(
		ds,
		core.EventTypeWarning,
		eventer.EventReasonFailedToStart,
		`Failed to complete operation for Datasource: "%v". Reason: %v`,
		ds.Name,
		reason,
	)
	ds, err := util.UpdateDatasourceStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), ds.ObjectMeta, func(in *api.DatasourceStatus) *api.DatasourceStatus {
		in.Phase = api.GrafanaPhaseFailed
		in.Reason = reason
		in.Conditions = kmapi.SetCondition(in.Conditions, kmapi.Condition{
			Type:    kmapi.ConditionFailed,
			Status:  core.ConditionTrue,
			Reason:  reason,
			Message: reason,
		})
		in.ObservedGeneration = ds.Generation
		return in
	}, metav1.UpdateOptions{})
	if err != nil {
		c.recorder.Eventf(
			ds,
			core.EventTypeWarning,
			eventer.EventReasonFailedToUpdate,
			err.Error(),
		)
	}
}
