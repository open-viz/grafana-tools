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
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	api "go.searchlight.dev/grafana-operator/apis/grafana/v1alpha1"
	"go.searchlight.dev/grafana-operator/client/clientset/versioned/typed/grafana/v1alpha1/util"
	"go.searchlight.dev/grafana-operator/pkg/eventer"

	"github.com/appscode/go/types"
	"github.com/golang/glog"
	"github.com/grafana-tools/sdk"
	"github.com/pkg/errors"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	core_util "kmodules.xyz/client-go/core/v1"
	meta_util "kmodules.xyz/client-go/meta"
	"kmodules.xyz/client-go/tools/queue"
	"kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
)

const (
	DashboardFinalizer = "dahsboard.grafana.searchlight.dev"
)

func (c *GrafanaController) initDashboardWatcher() {
	c.dashboardInformer = c.extInformerFactory.Grafana().V1alpha1().Dashboards().Informer()
	c.dashboardQueue = queue.New(api.ResourceKindDashboard, c.MaxNumRequeues, c.NumThreads, c.runDashboardInjector)
	c.dashboardInformer.AddEventHandler(queue.NewReconcilableHandler(c.dashboardQueue.GetQueue()))
	c.dashboardLister = c.extInformerFactory.Grafana().V1alpha1().Dashboards().Lister()
}

// runDashboardInjector gets the vault policy object indexed by the key from cache
// and initializes, reconciles or garbage collects the vault policy as needed.
func (c *GrafanaController) runDashboardInjector(key string) error {
	obj, exists, err := c.dashboardInformer.GetIndexer().GetByKey(key)
	if err != nil {
		glog.Errorf("Fetching object with key %s from store failed with %v", key, err)
		return err
	}

	if !exists {
		glog.Warningf("Dashboard %s does not exist anymore\n", key)
	} else {
		dashboard := obj.(*api.Dashboard).DeepCopy()
		glog.Infof("Sync/Add/Update for Dashboard %s/%s\n", dashboard.Namespace, dashboard.Name)

		if dashboard.DeletionTimestamp != nil {
			if core_util.HasFinalizer(dashboard.ObjectMeta, DashboardFinalizer) {
				// Finalize Dashboard
				go c.runDashboardFinalizer(dashboard)
			} else {
				glog.Infof("Finalizer not found for GrafanaDashboard %s/%s", dashboard.Namespace, dashboard.Name)
			}
		} else {
			if !core_util.HasFinalizer(dashboard.ObjectMeta, DashboardFinalizer) {
				// Add finalizer
				_, _, err := util.PatchDashboard(c.extClient.GrafanaV1alpha1(), dashboard, func(vp *api.Dashboard) *api.Dashboard {
					vp.ObjectMeta = core_util.AddFinalizer(dashboard.ObjectMeta, DashboardFinalizer)
					return vp
				})
				if err != nil {
					return errors.Wrapf(err, "failed to set Dashboard finalizer for %s/%s", dashboard.Namespace, dashboard.Name)
				}
			}
			if !meta_util.MustAlreadyReconciled(dashboard) {
				err = c.reconcileDashboard(dashboard)
				if err != nil {
					c.pushFailureEvent(dashboard, err.Error())
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
	newDashboard, err := util.UpdateDashboardStatus(c.extClient.GrafanaV1alpha1(), dashboard, func(in *api.DashboardStatus) *api.DashboardStatus {
		in.Phase = api.DashboardPhaseProcessing
		in.Reason = "Started processing of dashboard"
		in.Conditions = []api.DashboardCondition{}
		in.ObservedGeneration = dashboard.Generation

		return in
	})
	if err != nil {
		return errors.Wrapf(err, "failed to update dashboard phase to `%s`", api.DashboardPhaseSuccess)
	}
	dashboard.Status = newDashboard.Status

	if err = c.setGrafanaClient(dashboard); err != nil {
		return errors.Wrap(err, "failed to set grafana client")
	}

	var board sdk.Board
	if dashboard.Spec.Model == nil {
		return errors.New("dashboard model not found")
	}

	if err := json.Unmarshal(dashboard.Spec.Model.Raw, &board); err != nil {
		return err
	}
	if err = c.updateDashboard(dashboard, board); err != nil {
		return errors.Wrap(err, "failed to update dashboard")
	}

	return nil
}

// runDashboardFinalizer performs what need to be done
// before the dashboard is deleted
func (c *GrafanaController) runDashboardFinalizer(dashboard *api.Dashboard) {
	// update Terminating status
	newDashboard, err := util.UpdateDashboardStatus(c.extClient.GrafanaV1alpha1(), dashboard, func(in *api.DashboardStatus) *api.DashboardStatus {
		in.Phase = api.DashboardPhaseTerminating
		in.Reason = "Terminating dashboard"
		in.ObservedGeneration = dashboard.Generation

		return in
	})
	if err != nil {
		glog.Infof("Failed to update dashboard phase to `%s`. Reason: %v", api.DashboardPhaseTerminating, err)
	}
	dashboard.Status = newDashboard.Status

	if dashboard.Status.Dashboard != nil {
		_, err := c.grafanaClient.DeleteDashboard(types.String(dashboard.Status.Dashboard.Slug))
		if err != nil {
			c.recorder.Eventf(
				dashboard,
				core.EventTypeWarning,
				eventer.EventReasonFailedToDelete,
				`Failed to delete GrafanaDashboard: "%v". Reason: %v`,
				dashboard.Name,
				err.Error(),
			)

			glog.Infof("Failed to delete GrafanaDashboard: %v. Reason: %v", dashboard.Name, err)
			return
		}
	}

	_, _, err = util.PatchDashboard(c.extClient.GrafanaV1alpha1(), dashboard, func(in *api.Dashboard) *api.Dashboard {
		in.ObjectMeta = core_util.RemoveFinalizer(dashboard.ObjectMeta, DashboardFinalizer)
		return in
	})
	if err != nil {
		glog.Infof("Failed to set Dashboard finalizer for %s/%s. Reason: %v.", dashboard.Namespace, dashboard.Name, err)
	}
}

// updateDashboard updates dashboard database through api request
func (c *GrafanaController) updateDashboard(dashboard *api.Dashboard, board sdk.Board) error {
	if dashboard.Status.Dashboard != nil {
		board.ID = uint(types.Int64(dashboard.Status.Dashboard.ID))
		board.UID = types.String(dashboard.Status.Dashboard.UID)
	}

	statusMsg, err := c.grafanaClient.SetDashboard(board, int(dashboard.Spec.FolderID), dashboard.Spec.Overwrite)
	if err != nil {
		return errors.Wrap(err, "failed to save dashboard in grafana server")
	}

	newDashboard, err := util.UpdateDashboardStatus(c.extClient.GrafanaV1alpha1(), dashboard, func(in *api.DashboardStatus) *api.DashboardStatus {
		in.Phase = api.DashboardPhaseSuccess
		in.Reason = "Successfully completed the modification process"
		in.ObservedGeneration = dashboard.Generation

		in.Dashboard = &api.DashboardReference{
			ID:      types.Int64P(int64(types.UInt(statusMsg.ID))),
			UID:     statusMsg.UID,
			Slug:    statusMsg.Slug,
			URL:     statusMsg.URL,
			Version: types.Int64P(int64(types.Int(statusMsg.Version))),
		}
		if statusMsg.OrgID != nil {
			in.Dashboard.OrgID = types.Int64P(int64(types.UInt(statusMsg.OrgID)))
		}

		return in
	})
	if err != nil {
		return errors.Wrapf(err, "failed to update dashboard phase to `%s`", api.DashboardPhaseSuccess)
	}
	dashboard.Status = newDashboard.Status

	return nil
}

// setGrafanaClient sets grafana client from user provided data
func (c *GrafanaController) setGrafanaClient(dashboard *api.Dashboard) error {
	appBinding, err := c.appCatalogClient.AppBindings(dashboard.Namespace).Get(dashboard.Spec.Grafana.Name, metav1.GetOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to fetch AppBinding")
	}

	secret, err := c.kubeClient.CoreV1().Secrets(dashboard.Namespace).Get(appBinding.Spec.Secret.Name, metav1.GetOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to fetch Secret")
	}

	apiURL, apiKey, err := getApiURLandApiKey(appBinding, secret)
	if err != nil {
		return errors.Wrap(err, "failed to get apiURL or apiKey")
	}
	c.grafanaClient = sdk.NewClient(apiURL, apiKey, sdk.DefaultHTTPClient)

	return wait.PollImmediate(100*time.Millisecond, 1*time.Minute, func() (bool, error) {
		err := c.grafanaClient.CheckHealth()
		return err == nil, nil
	})
}

// getApiURLandApiKey extracts ApiURL and ApiKey from appBinding and secret
func getApiURLandApiKey(appBinding *v1alpha1.AppBinding, secret *core.Secret) (apiURL, apiKey string, err error) {
	cfg := appBinding.Spec.ClientConfig
	if cfg.URL != nil {
		apiURL = types.String(cfg.URL)
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
