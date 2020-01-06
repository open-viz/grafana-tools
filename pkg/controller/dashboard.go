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
	api "go.searchlight.dev/grafana-operator/apis/grafana/v1alpha1"
	"go.searchlight.dev/grafana-operator/client/clientset/versioned/typed/grafana/v1alpha1/util"

	"github.com/golang/glog"
	"github.com/pkg/errors"
	core_util "kmodules.xyz/client-go/core/v1"
	"kmodules.xyz/client-go/tools/queue"
)

const (
	DashboardFinalizer = "policy.grafana.searchlight.dev"
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
		vPolicy := obj.(*api.Dashboard).DeepCopy()
		glog.Infof("Sync/Add/Update for Dashboard %s/%s\n", vPolicy.Namespace, vPolicy.Name)

		if vPolicy.DeletionTimestamp != nil {
		} else {
			if !core_util.HasFinalizer(vPolicy.ObjectMeta, DashboardFinalizer) {
				// Add finalizer
				_, _, err := util.PatchDashboard(c.extClient.GrafanaV1alpha1(), vPolicy, func(vp *api.Dashboard) *api.Dashboard {
					vp.ObjectMeta = core_util.AddFinalizer(vPolicy.ObjectMeta, DashboardFinalizer)
					return vp
				})
				if err != nil {
					return errors.Wrapf(err, "failed to set Dashboard finalizer for %s/%s", vPolicy.Namespace, vPolicy.Name)
				}
			}

			err = c.reconcilePolicy(vPolicy)
			if err != nil {
				return errors.Wrapf(err, "for Dashboard %s/%s", vPolicy.Namespace, vPolicy.Name)
			}
		}
	}
	return nil
}

func (c *GrafanaController) reconcilePolicy(vPolicy *api.Dashboard) error {
	return nil
}
