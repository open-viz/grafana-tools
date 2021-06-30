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

	"github.com/golang/glog"
	"github.com/grafana-tools/sdk"
	api "go.searchlight.dev/grafana-operator/apis/grafana/v1alpha1"
	"kmodules.xyz/client-go/tools/queue"
)

const (
	DatasourceFinalizer = "datasource.grafana.searchlight.dev"
)

func (c *GrafanaController) initDatasourceWatcher() {
	c.datasourceInformer = c.extInformerFactory.Grafana().V1alpha1().Datasources().Informer()
	c.datasourceQueue = queue.New(api.ResourceKindDatasource, c.MaxNumRequeues, c.NumThreads, c.runDatasourceInjector)
	c.datasourceInformer.AddEventHandler(queue.NewReconcilableHandler(c.datasourceQueue.GetQueue()))
	c.datasourceLister = c.extInformerFactory.Grafana().V1alpha1().Datasources().Lister()
}

func (c *GrafanaController) runDatasourceInjector(key string) error {
	obj, exists, err := c.datasourceInformer.GetIndexer().GetByKey(key)
	if err != nil {
		glog.Errorf("Fetching object with key %s from store failed with %v", key, err)
		return err
	}
	if !exists {
		glog.Warningf("Datasource %s does not exist anymore\n", key)
	} else {
		ds := obj.(*api.Datasource)
		glog.Infof("Sync/Add/Update for Datasource %s/%s\n", ds.Namespace, ds.Name)
		err = c.CreateDataSource(ds)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *GrafanaController) CreateDataSource(ds *api.Datasource) error {
	dataSrc := sdk.Datasource{
		OrgID:     uint(ds.Spec.OrgID),
		Name:      ds.Spec.Name,
		Type:      ds.Spec.Type,
		Access:    ds.Spec.Access,
		URL:       ds.Spec.URL,
		IsDefault: ds.Spec.IsDefault,
	}
	err := c.setGrafanaClient(ds.Namespace, ds.Spec.Grafana)
	if err != nil {
		return err
	}
	_, err = c.grafanaClient.CreateDatasource(context.TODO(), dataSrc)
	if err != nil {
		return err
	}
	return nil
}
