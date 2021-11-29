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

	api "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/client/clientset/versioned/typed/openviz/v1alpha1/util"

	"github.com/grafana-tools/sdk"
	"github.com/pkg/errors"
	"gomodules.xyz/pointer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	core_util "kmodules.xyz/client-go/core/v1"
	"kmodules.xyz/client-go/tools/queue"
)

const (
	GrafanaDatasourceFinalizer = "grafanadatasource.openviz.dev"
)

func (c *GrafanaController) initGrafanaDatasourceWatcher() {
	c.grafanadatasourceInformer = c.extInformerFactory.Openviz().V1alpha1().GrafanaDatasources().Informer()
	c.grafanadatasourceQueue = queue.New(api.ResourceKindGrafanaDatasource, c.MaxNumRequeues, c.NumThreads, c.runGrafanaDatasourceInjector)
	c.grafanadatasourceInformer.AddEventHandler(queue.NewReconcilableHandler(c.grafanadatasourceQueue.GetQueue(), metav1.NamespaceAll))
	c.grafanadatasourceLister = c.extInformerFactory.Openviz().V1alpha1().GrafanaDatasources().Lister()
}

func (c *GrafanaController) runGrafanaDatasourceInjector(key string) error {
	obj, exists, err := c.grafanadatasourceInformer.GetIndexer().GetByKey(key)
	if err != nil {
		klog.Errorf("Fetching object with key %s from store failed with %v", key, err)
		return err
	}
	if !exists {
		klog.Warningf("GrafanaDatasource %s does not exist anymore\n", key)
	} else {
		ds := obj.(*api.GrafanaDatasource).DeepCopy()
		klog.Infof("Sync/Add/Update for GrafanaDatasource %s/%s\n", ds.Namespace, ds.Name)
		err = c.reconcileGrafanaDatasource(ds)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *GrafanaController) reconcileGrafanaDatasource(ds *api.GrafanaDatasource) error {
	// Update GrafanaDatasource Status to processing
	updatedDS, err := util.UpdateGrafanaDatasourceStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), ds.ObjectMeta, func(st *api.GrafanaDatasourceStatus) *api.GrafanaDatasourceStatus {
		st.Phase = api.GrafanaPhaseProcessing
		st.Reason = "Started processing GrafanaDatasource"
		st.ObservedGeneration = ds.Generation
		return st
	}, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to update GrafanaDatasource phase to %q\n", api.GrafanaPhaseProcessing)
	}
	ds.Status = updatedDS.Status

	if ds.DeletionTimestamp != nil {
		if core_util.HasFinalizer(ds.ObjectMeta, GrafanaDatasourceFinalizer) {
			err := c.runGrafanaDatasourceFinalizer(ds)
			if err != nil {
				return err
			}
		}
		return nil
	}
	if !core_util.HasFinalizer(ds.ObjectMeta, GrafanaDatasourceFinalizer) {
		// Add Finalizer
		updatedDS, _, err := util.PatchGrafanaDatasource(context.TODO(), c.extClient.OpenvizV1alpha1(), ds, func(up *api.GrafanaDatasource) *api.GrafanaDatasource {
			up.ObjectMeta = core_util.AddFinalizer(ds.ObjectMeta, GrafanaDatasourceFinalizer)
			return up
		}, metav1.PatchOptions{})
		if err != nil {
			return err
		}
		ds = updatedDS
	}
	err = c.createOrUpdateGrafanaDatasource(ds)
	if err != nil {
		return err
	}
	return nil
}

func (c *GrafanaController) createOrUpdateGrafanaDatasource(ds *api.GrafanaDatasource) error {
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

	if ds.Status.GrafanaDatasourceID != nil {
		err := c.updateGrafanaDatasource(ds, dataSrc)
		if err != nil {
			return errors.Wrapf(err, "can't update GrafanaDatasource, reason: %v", err)
		}
		return nil
	}
	statusMsg, err := c.grafanaClient.CreateDatasource(context.TODO(), dataSrc)
	if err != nil {
		c.pushGrafanaDatasourceFailureEvent(ds, err.Error())
		return err
	}
	klog.Infof("GrafanaDatasource is created with message: %s\n", pointer.String(statusMsg.Message))
	_, err = util.UpdateGrafanaDatasourceStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), ds.ObjectMeta, func(st *api.GrafanaDatasourceStatus) *api.GrafanaDatasourceStatus {
		st.Phase = api.GrafanaPhaseSuccess
		st.Reason = "Successfully created Grafana GrafanaDatasource"
		st.ObservedGeneration = ds.Generation
		st.GrafanaDatasourceID = pointer.Int64P(int64(pointer.Uint(statusMsg.ID)))

		return st
	}, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to update GrafanaDatasource phase to %q\n", api.GrafanaPhaseSuccess)
	}
	return nil
}

func (c *GrafanaController) updateGrafanaDatasource(ds *api.GrafanaDatasource, dataSrc sdk.Datasource) error {
	dataSrc.ID = uint(pointer.Int64(ds.Status.GrafanaDatasourceID))

	statusMsg, err := c.grafanaClient.UpdateDatasource(context.TODO(), dataSrc)
	if err != nil {
		c.pushGrafanaDatasourceFailureEvent(ds, err.Error())
		return err
	}
	klog.Infof("GrafanaDatasource is updated with message: %s\n", pointer.String(statusMsg.Message))
	// Update status to Success
	_, err = util.UpdateGrafanaDatasourceStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), ds.ObjectMeta, func(st *api.GrafanaDatasourceStatus) *api.GrafanaDatasourceStatus {
		st.Phase = api.GrafanaPhaseSuccess
		st.Reason = "Successfully updated Grafana GrafanaDatasource"
		st.ObservedGeneration = ds.Generation

		return st
	}, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to update GrafanaDatasource phase to %q\n", api.GrafanaPhaseSuccess)
	}
	return nil
}

func (c *GrafanaController) runGrafanaDatasourceFinalizer(ds *api.GrafanaDatasource) error {
	newDS, err := util.UpdateGrafanaDatasourceStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), ds.ObjectMeta, func(st *api.GrafanaDatasourceStatus) *api.GrafanaDatasourceStatus {
		st.Phase = api.GrafanaPhaseTerminating
		st.Reason = "Terminating GrafanaDatasource"
		st.ObservedGeneration = ds.Generation

		return st
	}, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to update GrafanaDatasource phase to %q\n", api.GrafanaPhaseTerminating)
	}
	ds.Status = newDS.Status

	if ds.Status.GrafanaDatasourceID != nil {
		dsID := uint(pointer.Int64(ds.Status.GrafanaDatasourceID))
		statusMsg, err := c.grafanaClient.DeleteDatasource(context.TODO(), dsID)
		if err != nil {
			c.pushGrafanaDatasourceFailureEvent(ds, err.Error())
			return err
		}
		klog.Infof("GrafanaDatasource is deleted with message: %s\n", pointer.String(statusMsg.Message))
	} else if ds.Status.Phase == api.GrafanaPhaseSuccess {
		return errors.New("grafanadatasource can't be deleted: reason: GrafanaDatasource ID is missing")
	}

	// if .status.GrafanaDatasourceID is nil and phase is not success
	// so the remote grafana object is never created.
	// so the finalizer can be removed.

	// remove Finalizer
	_, _, err = util.PatchGrafanaDatasource(context.TODO(), c.extClient.OpenvizV1alpha1(), ds, func(up *api.GrafanaDatasource) *api.GrafanaDatasource {
		up.ObjectMeta = core_util.RemoveFinalizer(ds.ObjectMeta, GrafanaDatasourceFinalizer)
		return up
	}, metav1.PatchOptions{})
	if err != nil {
		return err
	}
	return nil
}
