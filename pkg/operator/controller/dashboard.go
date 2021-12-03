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

	sdk "go.openviz.dev/grafana-sdk"
	api "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/client/clientset/versioned/typed/openviz/v1alpha1/util"
	"go.openviz.dev/grafana-tools/pkg/operator/eventer"

	"github.com/pkg/errors"
	"gomodules.xyz/pointer"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	kmapi "kmodules.xyz/client-go/api/v1"
	core_util "kmodules.xyz/client-go/core/v1"
	meta_util "kmodules.xyz/client-go/meta"
	"kmodules.xyz/client-go/tools/queue"
	"kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
)

const (
	GrafanaDashboardFinalizer = "grafanadashboard.openviz.dev"
)

func (c *GrafanaController) initGrafanaDashboardWatcher() {
	c.grafanadashboardInformer = c.extInformerFactory.Openviz().V1alpha1().GrafanaDashboards().Informer()
	c.grafanadashboardQueue = queue.New(api.ResourceKindGrafanaDashboard, c.MaxNumRequeues, c.NumThreads, c.runGrafanaDashboardInjector)
	c.grafanadashboardInformer.AddEventHandler(queue.NewReconcilableHandler(c.grafanadashboardQueue.GetQueue(), metav1.NamespaceAll))
	c.grafanadashboardLister = c.extInformerFactory.Openviz().V1alpha1().GrafanaDashboards().Lister()
}

// runGrafanaDashboardInjector gets the vault policy object indexed by the key from cache
// and initializes, reconciles or garbage collects the vault policy as needed.
func (c *GrafanaController) runGrafanaDashboardInjector(key string) error {
	obj, exists, err := c.grafanadashboardInformer.GetIndexer().GetByKey(key)
	if err != nil {
		klog.Errorf("Fetching object with key %s from store failed with %v", key, err)
		return err
	}

	if !exists {
		klog.Warningf("GrafanaDashboard %s does not exist anymore\n", key)
	} else {
		grafanadashboard := obj.(*api.GrafanaDashboard).DeepCopy()
		klog.Infof("Sync/Add/Update for GrafanaDashboard %s/%s\n", grafanadashboard.Namespace, grafanadashboard.Name)

		if grafanadashboard.DeletionTimestamp != nil {
			if core_util.HasFinalizer(grafanadashboard.ObjectMeta, GrafanaDashboardFinalizer) {
				// Finalize GrafanaDashboard
				err := c.runGrafanaDashboardFinalizer(grafanadashboard)
				if err != nil {
					return err
				}
			} else {
				klog.Infof("Finalizer not found for GrafanaGrafanaDashboard %s/%s", grafanadashboard.Namespace, grafanadashboard.Name)
			}
			return nil
		} else {
			if !core_util.HasFinalizer(grafanadashboard.ObjectMeta, GrafanaDashboardFinalizer) {
				// Add finalizer
				_, _, err := util.PatchGrafanaDashboard(context.TODO(), c.extClient.OpenvizV1alpha1(), grafanadashboard, func(vp *api.GrafanaDashboard) *api.GrafanaDashboard {
					vp.ObjectMeta = core_util.AddFinalizer(grafanadashboard.ObjectMeta, GrafanaDashboardFinalizer)
					return vp
				}, metav1.PatchOptions{})
				if err != nil {
					return errors.Wrapf(err, "failed to set GrafanaDashboard finalizer for %s/%s", grafanadashboard.Namespace, grafanadashboard.Name)
				}
			}
			if !meta_util.MustAlreadyReconciled(grafanadashboard) {
				err = c.reconcileGrafanaDashboard(grafanadashboard)
				if err != nil {
					c.pushGrafanaDashboardFailureEvent(grafanadashboard, err.Error())
					return errors.Wrapf(err, "for GrafanaDashboard %s/%s", grafanadashboard.Namespace, grafanadashboard.Name)
				}
			}
		}
	}
	return nil
}

// reconcileGrafanaDashboard reconciles the grafana grafanadashboard
// it creates or updates grafanadashboard
func (c *GrafanaController) reconcileGrafanaDashboard(grafanadashboard *api.GrafanaDashboard) error {
	// update Processing status
	newGrafanaDashboard, err := util.UpdateGrafanaDashboardStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), grafanadashboard.ObjectMeta, func(in *api.GrafanaDashboardStatus) *api.GrafanaDashboardStatus {
		in.Phase = api.GrafanaPhaseProcessing
		in.Reason = "Started processing of grafanadashboard"
		in.Conditions = []kmapi.Condition{}
		in.ObservedGeneration = grafanadashboard.Generation

		return in
	}, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to update grafanadashboard phase to `%s`", api.GrafanaPhaseSuccess)
	}
	grafanadashboard.Status = newGrafanaDashboard.Status

	if grafanadashboard.Spec.Grafana == nil {
		return errors.New("appBinding for grafana is missing")
	}

	if err = c.setGrafanaClient(grafanadashboard.Namespace, grafanadashboard.Spec.Grafana); err != nil {
		return errors.Wrap(err, "failed to set grafana client")
	}

	if grafanadashboard.Spec.Model == nil {
		return errors.New("grafanadashboard model not found")
	}
	model := grafanadashboard.Spec.Model.Raw

	//// add labels
	//labels := make(map[string]string)
	//labels["meta.grafanadashboard.openviz.dev/appbinding"] = grafanadashboard.Spec.Grafana.Name
	//grafanadashboard, _, err = util.PatchGrafanaDashboard(context.TODO(), c.extClient.OpenvizV1alpha1(), grafanadashboard, func(in *api.GrafanaDashboard) *api.GrafanaDashboard {
	//	in.ObjectMeta.SetLabels(labels)
	//	return in
	//}, metav1.PatchOptions{})
	//if err != nil {
	//	return err
	//}

	// collect grafanadatasource name from app binding
	appBinding, err := c.appCatalogClient.AppBindings(grafanadashboard.Namespace).Get(context.TODO(), grafanadashboard.Spec.Grafana.Name, metav1.GetOptions{})
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
		return errors.New("grafanadatasource parameter is missing in app binding")
	}

	if grafanadashboard.Spec.Templatize != nil && grafanadashboard.Spec.Templatize.Datasource {
		model, err = replaceDatasource(model, dsConfig.Datasource)
		if err != nil {
			return err
		}
	}

	// update folder when it is missing
	if grafanadashboard.Spec.FolderID == nil {
		if dsConfig.FolderID != nil {
			grafanadashboard.Spec.FolderID = dsConfig.FolderID
		} else {
			// set default(General) folder id: 0
			grafanadashboard.Spec.FolderID = pointer.Int64P(0)
		}
	}

	if err = c.updateGrafanaDashboard(grafanadashboard, model); err != nil {
		return errors.Wrap(err, "failed to update grafanadashboard")
	}

	return nil
}

// runGrafanaDashboardFinalizer performs what need to be done
// before the grafanadashboard is deleted
func (c *GrafanaController) runGrafanaDashboardFinalizer(grafanadashboard *api.GrafanaDashboard) error {
	// update Terminating status
	newGrafanaDashboard, err := util.UpdateGrafanaDashboardStatus(context.TODO(), c.extClient.OpenvizV1alpha1(), grafanadashboard.ObjectMeta, func(in *api.GrafanaDashboardStatus) *api.GrafanaDashboardStatus {
		in.Phase = api.GrafanaPhaseTerminating
		in.Reason = "Terminating grafanadashboard"
		in.ObservedGeneration = grafanadashboard.Generation

		return in
	}, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrapf(err, "failed to update grafanadashboard phase to %q\n", api.GrafanaPhaseTerminating)
	}
	grafanadashboard.Status = newGrafanaDashboard.Status

	if grafanadashboard.Status.Dashboard != nil && grafanadashboard.Status.Dashboard.UID != nil {
		statusMsg, err := c.grafanaClient.DeleteDashboardByUID(context.TODO(), pointer.String(grafanadashboard.Status.Dashboard.UID))
		if err != nil {
			c.recorder.Eventf(
				grafanadashboard,
				core.EventTypeWarning,
				eventer.EventReasonFailedToDelete,
				`Failed to delete GrafanaGrafanaDashboard: "%v". Reason: %v`,
				grafanadashboard.Name,
				err.Error(),
			)

			klog.Infof("Failed to delete GrafanaGrafanaDashboard: %v. Reason: %v", grafanadashboard.Name, err)
			return err
		}
		klog.Infof("GrafanaDashboard is deleted with message: %s\n", pointer.String(statusMsg.Message))
	} else if grafanadashboard.Status.Phase == api.GrafanaPhaseSuccess {
		return errors.New("finalizer can't be removed. reason: GrafanaDashboard UID is missing")
	}

	// if .status.GrafanaDatasourceID is nil and phase is not success
	// so the remote grafana object is never created.
	// so the finalizer can be removed.

	// remove Finalizer
	_, _, err = util.PatchGrafanaDashboard(context.TODO(), c.extClient.OpenvizV1alpha1(), grafanadashboard, func(in *api.GrafanaDashboard) *api.GrafanaDashboard {
		in.ObjectMeta = core_util.RemoveFinalizer(grafanadashboard.ObjectMeta, GrafanaDashboardFinalizer)
		return in
	}, metav1.PatchOptions{})
	if err != nil {
		return err
	}
	return nil
}

// updateGrafanaDashboard updates grafanadashboard database through api request
func (c *GrafanaController) updateGrafanaDashboard(grafanadashboard *api.GrafanaDashboard, model []byte) error {
	var err error
	if grafanadashboard.Status.Dashboard != nil {
		model, err = addDashboardID(model, *grafanadashboard.Status.Dashboard.ID, *grafanadashboard.Status.Dashboard.UID)
		if err != nil {
			return err
		}
	}
	gDB := &sdk.GrafanaDashboard{
		Dashboard: &runtime.RawExtension{Raw: model},
		FolderId:  int(pointer.Int64(grafanadashboard.Spec.FolderID)),
		Overwrite: grafanadashboard.Spec.Overwrite,
	}
	resp, err := c.grafanaClient.SetDashboard(context.Background(), gDB)
	if err != nil {
		return errors.Wrap(err, "failed to save grafanadashboard in grafana server")
	}
	orgId, err := c.grafanaClient.GetCurrentOrg(context.Background())
	if err != nil {
		return errors.Wrap(err, "failed to get OrgId")
	}

	newGrafanaDashboard, err := util.UpdateGrafanaDashboardStatus(
		context.TODO(),
		c.extClient.OpenvizV1alpha1(),
		grafanadashboard.ObjectMeta,
		func(in *api.GrafanaDashboardStatus) *api.GrafanaDashboardStatus {
			in.Phase = api.GrafanaPhaseSuccess
			in.Reason = "Successfully completed the modification process"
			in.ObservedGeneration = grafanadashboard.Generation

			in.Dashboard = &api.GrafanaDashboardReference{
				ID:      pointer.Int64P(int64(pointer.Int(resp.ID))),
				UID:     resp.UID,
				Slug:    resp.Slug,
				URL:     resp.URL,
				OrgID:   pointer.Int64P(int64(pointer.Int(orgId.ID))),
				Version: pointer.Int64P(int64(pointer.Int(resp.Version))),
			}

			return in
		},
		metav1.UpdateOptions{},
	)
	if err != nil {
		return errors.Wrapf(err, "failed to update grafanadashboard phase to `%s`", api.GrafanaPhaseSuccess)
	}
	grafanadashboard.Status = newGrafanaDashboard.Status

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
	c.grafanaClient, err = sdk.NewClient(apiURL, apiKey)
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

func replaceDatasource(model []byte, ds string) ([]byte, error) {
	val := make(map[string]interface{})
	err := json.Unmarshal(model, &val)
	if err != nil {
		return nil, err
	}
	panels, ok := val["panels"].([]interface{})
	if !ok {
		return model, nil
	}
	var updatedPanels []interface{}
	for _, p := range panels {
		panel, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		panel["datasource"] = ds
		updatedPanels = append(updatedPanels, panel)
	}
	val["panels"] = updatedPanels

	templateList, ok := val["templating"].(map[string]interface{})
	if !ok {
		return json.Marshal(val)
	}
	templateVars, ok := templateList["list"].([]interface{})
	if !ok {
		return json.Marshal(val)
	}

	var newVars []interface{}
	for _, v := range templateVars {
		vr, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		ty, ok := vr["type"].(string)
		if !ok {
			continue
		}
		vr["datasource"] = ds
		if ty != "datasource" {
			newVars = append(newVars, vr)
		}
	}
	templateList["list"] = newVars

	return json.Marshal(val)
}

func addDashboardID(model []byte, id int64, uid string) ([]byte, error) {
	val := make(map[string]interface{})
	err := json.Unmarshal(model, &val)
	if err != nil {
		return nil, err
	}
	val["id"] = id
	val["uid"] = uid
	return json.Marshal(val)
}
