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

	api "go.openviz.dev/grafana-operator/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-operator/client/clientset/versioned/typed/openviz/v1alpha1/util"

	"github.com/grafana-tools/sdk"
	"github.com/pkg/errors"
	"gomodules.xyz/pointer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	core_util "kmodules.xyz/client-go/core/v1"
	"kmodules.xyz/client-go/tools/queue"
)

const (
	DatasourceFinalizer = "datasource.openviz.dev"
)

func (c *GrafanaController) initDatasourceWatcher() {
	c.datasourceInformer = c.extInformerFactory.Openviz().V1alpha1().Datasources().Informer()
	c.datasourceQueue = queue.New(api.ResourceKindDatasource, c.MaxNumRequeues, c.NumThreads, c.runDatasourceInjector)
	c.datasourceInformer.AddEventHandler(queue.NewReconcilableHandler(c.datasourceQueue.GetQueue(), metav1.NamespaceAll))
	c.datasourceLister = c.extInformerFactory.Openviz().V1alpha1().Datasources().Lister()
}

func (c *GrafanaController) runDatasourceInjector(key string) error {
	obj, exists, err := c.datasourceInformer.GetIndexer().GetByKey(key)
	if err != nil {
		klog.Errorf("Fetching object with key %s from store failed with %v", key, err)
		return err
	}
	if !exists {
		klog.Warningf("Datasource %s does not exist anymore\n", key)
	} else {
		ds := obj.(*api.Datasource).DeepCopy()
		klog.Infof("Sync/Add/Update for Datasource %s/%s\n", ds.Namespace, ds.Name)
		err = c.reconcileDatasource(ds)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *GrafanaController) reconcileDatasource(ds *api.Datasource) error {
	// Update Datasource Status to processing
	updatedDS, err := util.UpdateDatasourceStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), ds.ObjectMeta, func(st *api.DatasourceStatus) *api.DatasourceStatus {
		st.Phase = api.GrafanaPhaseProcessing
		st.Reason = "Started processing Datasource"
		st.ObservedGeneration = ds.Generation
		return st
	}, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to update Datasource phase to %q\n", api.GrafanaPhaseProcessing)
	}
	ds.Status = updatedDS.Status

	if ds.DeletionTimestamp != nil {
		if core_util.HasFinalizer(ds.ObjectMeta, DatasourceFinalizer) {
			err := c.runDatasourceFinalizer(ds)
			if err != nil {
				return err
			}
		}
		return nil
	}
	if !core_util.HasFinalizer(ds.ObjectMeta, DatasourceFinalizer) {
		// Add Finalizer
		updatedDS, _, err := util.PatchDatasource(context.TODO(), c.extClient.OpenvizV1alpha1(), ds, func(up *api.Datasource) *api.Datasource {
			up.ObjectMeta = core_util.AddFinalizer(ds.ObjectMeta, DatasourceFinalizer)
			return up
		}, metav1.PatchOptions{})
		if err != nil {
			return err
		}
		ds = updatedDS
	}
	err = c.createOrUpdateDatasource(ds)
	if err != nil {
		return err
	}
	return nil
}

func (c *GrafanaController) createOrUpdateDatasource(ds *api.Datasource) error {
	dataSrc := sdk.Datasource{
		OrgID:     uint(ds.Spec.OrgID),
		Name:      ds.Spec.Name,
		Type:      string(ds.Spec.Type),
		Access:    string(ds.Spec.Access),
		URL:       ds.Spec.URL,
		IsDefault: ds.Spec.IsDefault,
	}

	err := c.setGrafanaClient(ds.Namespace, ds.Spec.Grafana)
	if err != nil {
		return err
	}

	if ds.Status.DatasourceID != nil {
		err := c.updateDatasource(ds, dataSrc)
		if err != nil {
			return errors.Wrapf(err, "can't update Datasource, reason: %v", err)
		}
		return nil
	}
	statusMsg, err := c.grafanaClient.CreateDatasource(context.TODO(), dataSrc)
	if err != nil {
		c.pushDatasourceFailureEvent(ds, err.Error())
		return err
	}
	klog.Infof("Datasource is created with message: %s\n", pointer.String(statusMsg.Message))
	_, err = util.UpdateDatasourceStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), ds.ObjectMeta, func(st *api.DatasourceStatus) *api.DatasourceStatus {
		st.Phase = api.GrafanaPhaseSuccess
		st.Reason = "Successfully created Grafana Datasource"
		st.ObservedGeneration = ds.Generation
		st.DatasourceID = pointer.Int64P(int64(pointer.Uint(statusMsg.ID)))

		return st
	}, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to update Datasource phase to %q\n", api.GrafanaPhaseSuccess)
	}
	return nil
}

func (c *GrafanaController) updateDatasource(ds *api.Datasource, dataSrc sdk.Datasource) error {
	dataSrc.ID = uint(pointer.Int64(ds.Status.DatasourceID))

	statusMsg, err := c.grafanaClient.UpdateDatasource(context.TODO(), dataSrc)
	if err != nil {
		c.pushDatasourceFailureEvent(ds, err.Error())
		return err
	}
	klog.Infof("Datasource is updated with message: %s\n", pointer.String(statusMsg.Message))
	// Update status to Success
	_, err = util.UpdateDatasourceStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), ds.ObjectMeta, func(st *api.DatasourceStatus) *api.DatasourceStatus {
		st.Phase = api.GrafanaPhaseSuccess
		st.Reason = "Successfully updated Grafana Datasource"
		st.ObservedGeneration = ds.Generation

		return st
	}, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to update Datasource phase to %q\n", api.GrafanaPhaseSuccess)
	}
	return nil
}

func (c *GrafanaController) runDatasourceFinalizer(ds *api.Datasource) error {
	newDS, err := util.UpdateDatasourceStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), ds.ObjectMeta, func(st *api.DatasourceStatus) *api.DatasourceStatus {
		st.Phase = api.GrafanaPhaseTerminating
		st.Reason = "Terminating Datasource"
		st.ObservedGeneration = ds.Generation

		return st
	}, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to update Datasource phase to %q\n", api.GrafanaPhaseTerminating)
	}
	ds.Status = newDS.Status

	if ds.Status.DatasourceID == nil {
		return errors.New("datasource can't be deleted: reason: Datasource ID is missing")
	}
	dsID := uint(pointer.Int64(ds.Status.DatasourceID))
	statusMsg, err := c.grafanaClient.DeleteDatasource(context.TODO(), dsID)
	if err != nil {
		c.pushDatasourceFailureEvent(ds, err.Error())
		return err
	}
	klog.Infof("Datasource is deleted with message: %s\n", pointer.String(statusMsg.Message))

	// remove Finalizer
	_, _, err = util.PatchDatasource(context.TODO(), c.extClient.OpenvizV1alpha1(), ds, func(up *api.Datasource) *api.Datasource {
		up.ObjectMeta = core_util.RemoveFinalizer(ds.ObjectMeta, DatasourceFinalizer)
		return up
	}, metav1.PatchOptions{})
	if err != nil {
		return err
	}
	return nil
}
