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
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	api "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/client/clientset/versioned/typed/openviz/v1alpha1/util"
	"go.openviz.dev/grafana-tools/pkg/operator/eventer"

	"github.com/grafana-tools/sdk"
	"github.com/pkg/errors"
	"gomodules.xyz/pointer"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	kmapi "kmodules.xyz/client-go/api/v1"
	core_util "kmodules.xyz/client-go/core/v1"
	meta_util "kmodules.xyz/client-go/meta"
	"kmodules.xyz/client-go/tools/queue"
	"kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
)

const (
	DashboardFinalizer = "dashboard.openviz.dev"
)

func (c *GrafanaController) initDashboardWatcher() {
	c.dashboardInformer = c.extInformerFactory.Openviz().V1alpha1().Dashboards().Informer()
	c.dashboardQueue = queue.New(api.ResourceKindDashboard, c.MaxNumRequeues, c.NumThreads, c.runDashboardInjector)
	c.dashboardInformer.AddEventHandler(queue.NewReconcilableHandler(c.dashboardQueue.GetQueue(), metav1.NamespaceAll))
	c.dashboardLister = c.extInformerFactory.Openviz().V1alpha1().Dashboards().Lister()
}

// runDashboardInjector gets the vault policy object indexed by the key from cache
// and initializes, reconciles or garbage collects the vault policy as needed.
func (c *GrafanaController) runDashboardInjector(key string) error {
	obj, exists, err := c.dashboardInformer.GetIndexer().GetByKey(key)
	if err != nil {
		klog.Errorf("Fetching object with key %s from store failed with %v", key, err)
		return err
	}

	if !exists {
		klog.Warningf("Dashboard %s does not exist anymore\n", key)
	} else {
		dashboard := obj.(*api.Dashboard).DeepCopy()
		klog.Infof("Sync/Add/Update for Dashboard %s/%s\n", dashboard.Namespace, dashboard.Name)

		if dashboard.DeletionTimestamp != nil {
			if core_util.HasFinalizer(dashboard.ObjectMeta, DashboardFinalizer) {
				// Finalize Dashboard
				err := c.runDashboardFinalizer(dashboard)
				if err != nil {
					return err
				}
			} else {
				klog.Infof("Finalizer not found for GrafanaDashboard %s/%s", dashboard.Namespace, dashboard.Name)
			}
			return nil
		} else {
			if !core_util.HasFinalizer(dashboard.ObjectMeta, DashboardFinalizer) {
				// Add finalizer
				_, _, err := util.PatchDashboard(context.TODO(), c.extClient.OpenvizV1alpha1(), dashboard, func(vp *api.Dashboard) *api.Dashboard {
					vp.ObjectMeta = core_util.AddFinalizer(dashboard.ObjectMeta, DashboardFinalizer)
					return vp
				}, metav1.PatchOptions{})
				if err != nil {
					return errors.Wrapf(err, "failed to set Dashboard finalizer for %s/%s", dashboard.Namespace, dashboard.Name)
				}
			}
			if !meta_util.MustAlreadyReconciled(dashboard) {
				err = c.reconcileDashboard(dashboard)
				if err != nil {
					c.pushDashboardFailureEvent(dashboard, err.Error())
					return errors.Wrapf(err, "for Dashboard %s/%s", dashboard.Namespace, dashboard.Name)
				}
			}
		}
	}
	return nil
}

// reconcileDashboard reconciles the grafana dashboard
// it creates or updates dashboard
func (c *GrafanaController) reconcileDashboard(dashboard *api.Dashboard) error {
	// update Processing status
	newDashboard, err := util.UpdateDashboardStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), dashboard.ObjectMeta, func(in *api.DashboardStatus) *api.DashboardStatus {
		in.Phase = api.GrafanaPhaseProcessing
		in.Reason = "Started processing of dashboard"
		in.Conditions = []kmapi.Condition{}
		in.ObservedGeneration = dashboard.Generation

		return in
	}, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to update dashboard phase to `%s`", api.GrafanaPhaseSuccess)
	}
	dashboard.Status = newDashboard.Status

	if dashboard.Spec.Grafana == nil {
		return errors.New("appBinding for grafana is missing")
	}

	if err = c.setGrafanaClient(dashboard.Namespace, dashboard.Spec.Grafana); err != nil {
		return errors.Wrap(err, "failed to set grafana client")
	}

	var board sdk.Board
	if dashboard.Spec.Model == nil {
		return errors.New("dashboard model not found")
	}
	if err := json.Unmarshal(dashboard.Spec.Model.Raw, &board); err != nil {
		return err
	}

	//// add labels
	//labels := make(map[string]string)
	//labels["meta.dashboard.openviz.dev/appbinding"] = dashboard.Spec.Grafana.Name
	//dashboard, _, err = util.PatchDashboard(context.TODO(), c.extClient.OpenvizV1alpha1(), dashboard, func(in *api.Dashboard) *api.Dashboard {
	//	in.ObjectMeta.SetLabels(labels)
	//	return in
	//}, metav1.PatchOptions{})
	//if err != nil {
	//	return err
	//}

	if dashboard.Spec.Templatize != nil && dashboard.Spec.Templatize.Datasource {
		// collect datasource name from app binding
		appBinding, err := c.appCatalogClient.AppBindings(dashboard.Namespace).Get(context.TODO(), dashboard.Spec.Grafana.Name, metav1.GetOptions{})
		if err != nil {
			return errors.Wrap(err, "failed to fetch AppBinding")
		}
		dsConfig := &api.DatasourceConfiguration{}
		if appBinding.Spec.Parameters != nil {
			err := json.Unmarshal(appBinding.Spec.Parameters.Raw, &dsConfig)
			if err != nil {
				return errors.Wrap(err, "failed to unmarshal app binding parameters")
			}
		} else {
			return errors.New("datasource parameter is missing in app binding")
		}
		// update panels datasource
		for idx := range board.Panels {
			board.Panels[idx].Datasource = &dsConfig.Datasource
		}

		// update templating list datasource
		for idx := range board.Templating.List {
			board.Templating.List[idx].Datasource = &dsConfig.Datasource
		}
	}

	if err = c.updateDashboard(dashboard, board); err != nil {
		return errors.Wrap(err, "failed to update dashboard")
	}

	return nil
}

// runDashboardFinalizer performs what need to be done
// before the dashboard is deleted
func (c *GrafanaController) runDashboardFinalizer(dashboard *api.Dashboard) error {
	// update Terminating status
	newDashboard, err := util.UpdateDashboardStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), dashboard.ObjectMeta, func(in *api.DashboardStatus) *api.DashboardStatus {
		in.Phase = api.GrafanaPhaseTerminating
		in.Reason = "Terminating dashboard"
		in.ObservedGeneration = dashboard.Generation

		return in
	}, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to update dashboard phase to %q\n", api.GrafanaPhaseTerminating)
	}
	dashboard.Status = newDashboard.Status

	if dashboard.Status.Dashboard != nil && dashboard.Status.Dashboard.UID != nil {
		statusMsg, err := c.grafanaClient.DeleteDashboardByUID(context.TODO(), pointer.String(dashboard.Status.Dashboard.UID))
		if err != nil {
			c.recorder.Eventf(
				dashboard,
				core.EventTypeWarning,
				eventer.EventReasonFailedToDelete,
				`Failed to delete GrafanaDashboard: "%v". Reason: %v`,
				dashboard.Name,
				err.Error(),
			)

			klog.Infof("Failed to delete GrafanaDashboard: %v. Reason: %v", dashboard.Name, err)
			return err
		}
		klog.Infof("Dashboard is deleted with message: %s\n", pointer.String(statusMsg.Message))
	} else if dashboard.Status.Phase == api.GrafanaPhaseSuccess {
		return errors.New("finalizer can't be removed. reason: Dashboard UID is missing")
	}

	// if .status.DatasourceID is nil and phase is not success
	// so the remote grafana object is never created.
	// so the finalizer can be removed.

	// remove Finalizer
	_, _, err = util.PatchDashboard(context.TODO(), c.extClient.OpenvizV1alpha1(), dashboard, func(in *api.Dashboard) *api.Dashboard {
		in.ObjectMeta = core_util.RemoveFinalizer(dashboard.ObjectMeta, DashboardFinalizer)
		return in
	}, metav1.PatchOptions{})
	if err != nil {
		return err
	}
	return nil
}

// updateDashboard updates dashboard database through api request
func (c *GrafanaController) updateDashboard(dashboard *api.Dashboard, board sdk.Board) error {
	if dashboard.Status.Dashboard != nil {
		board.ID = uint(pointer.Int64(dashboard.Status.Dashboard.ID))
		board.UID = pointer.String(dashboard.Status.Dashboard.UID)
	}

	params := sdk.SetDashboardParams{
		FolderID:  int(dashboard.Spec.FolderID),
		Overwrite: dashboard.Spec.Overwrite,
	}
	statusMsg, err := c.grafanaClient.SetDashboard(context.Background(), board, params)
	if err != nil {
		return errors.Wrap(err, "failed to save dashboard in grafana server")
	}
	orgId, err := c.grafanaClient.GetActualOrg(context.Background())
	if err != nil {
		return errors.Wrap(err, "failed to get OrgId")
	}

	newDashboard, err := util.UpdateDashboardStatus(
		context.TODO(),
		c.extClient.OpenvizV1alpha1(),
		dashboard.ObjectMeta,
		func(in *api.DashboardStatus) *api.DashboardStatus {
			in.Phase = api.GrafanaPhaseSuccess
			in.Reason = "Successfully completed the modification process"
			in.ObservedGeneration = dashboard.Generation

			in.Dashboard = &api.DashboardReference{
				ID:      pointer.Int64P(int64(pointer.Uint(statusMsg.ID))),
				UID:     statusMsg.UID,
				Slug:    statusMsg.Slug,
				URL:     statusMsg.URL,
				OrgID:   pointer.Int64P(int64(orgId.ID)),
				Version: pointer.Int64P(int64(pointer.Int(statusMsg.Version))),
			}
			if statusMsg.OrgID != nil {
				in.Dashboard.OrgID = pointer.Int64P(int64(pointer.Uint(statusMsg.OrgID)))
			}

			return in
		},
		metav1.UpdateOptions{},
	)
	if err != nil {
		return errors.Wrapf(err, "failed to update dashboard phase to `%s`", api.GrafanaPhaseSuccess)
	}
	dashboard.Status = newDashboard.Status

	return nil
}

// setGrafanaClient sets grafana client from user provided data
func (c *GrafanaController) setGrafanaClient(ns string, targetRef *api.TargetRef) error {
	appBinding, err := c.appCatalogClient.AppBindings(ns).Get(context.TODO(), targetRef.Name, metav1.GetOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to fetch AppBinding")
	}

	secret, err := c.kubeClient.CoreV1().Secrets(ns).Get(context.TODO(), appBinding.Spec.Secret.Name, metav1.GetOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to fetch Secret")
	}

	apiURL, apiKey, err := getApiURLandApiKey(appBinding, secret)
	if err != nil {
		return errors.Wrap(err, "failed to get apiURL or apiKey")
	}
	c.grafanaClient, err = sdk.NewClient(apiURL, apiKey, sdk.DefaultHTTPClient)
	if err != nil {
		return err
	}

	return wait.PollImmediate(100*time.Millisecond, 1*time.Minute, func() (bool, error) {
		health, err := c.grafanaClient.GetHealth(context.Background())
		if err != nil {
			return false, nil
		}
		return health.Database == "ok", nil
	})
}

// getApiURLandApiKey extracts ApiURL and ApiKey from appBinding and secret
func getApiURLandApiKey(appBinding *v1alpha1.AppBinding, secret *core.Secret) (apiURL, apiKey string, err error) {
	cfg := appBinding.Spec.ClientConfig
	if cfg.URL != nil {
		apiURL = pointer.String(cfg.URL)
	} else if cfg.Service != nil {
		apiurl := url.URL{
			Scheme:   cfg.Service.Scheme,
			Host:     cfg.Service.Name + "." + appBinding.Namespace + "." + "svc" + ":" + strconv.Itoa(int(cfg.Service.Port)),
			Path:     cfg.Service.Path,
			RawQuery: cfg.Service.Query,
		}
		apiURL = apiurl.String()
	}
	apiKey = string(secret.Data["apiKey"])
	if len(apiURL) == 0 || len(apiKey) == 0 {
		err = fmt.Errorf("apiURL or apiKey not provided")
	}
	return
}
