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

package namespace

import (
	"context"
	"fmt"
	"time"

	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	"go.openviz.dev/grafana-tools/pkg/controllers/clientorg"

	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	kutil "kmodules.xyz/client-go"
	kmapi "kmodules.xyz/client-go/api/v1"
	cu "kmodules.xyz/client-go/client"
	core_util "kmodules.xyz/client-go/core/v1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// isActiveClientOrg reports whether the namespace is still a live, non-terminating client-org.
func isActiveClientOrg(ns *core.Namespace) bool {
	return ns.DeletionTimestamp == nil &&
		ns.Labels[kmapi.ClientOrgKey] != "" &&
		ns.Labels[kmapi.ClientOrgKey] != "terminating"
}

// handleDeletion unregisters the client-org from the grafana/perses backends, deletes
// everything this controller created in the client monitoring namespace, and drops the
// finalizer so the namespace can finish terminating.
func (r *ClientOrgReconciler) handleDeletion(ctx context.Context, ns core.Namespace, clientOrgId string) (ctrl.Result, error) {
	if r.pc != nil {
		err := r.pc.Unregister(mona.PrometheusContext{
			HubUID:      r.hubUID,
			ClusterUID:  r.clusterUID,
			ProjectId:   "",
			Default:     false,
			IssueToken:  true,
			ClientOrgID: clientOrgId,
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to unregister grafana backend for client-org %s: %w", clientOrgId, err)
		}

		err = r.pc.UnregisterPerses(mona.PrometheusContext{
			HubUID:      r.hubUID,
			ClusterUID:  r.clusterUID,
			ProjectId:   "",
			Default:     false,
			IssueToken:  true,
			ClientOrgID: clientOrgId,
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to unregister perses backend for client-org %s: %w", clientOrgId, err)
		}
		klog.Infof("unregistered grafana/perses backends for client-org %s", clientOrgId)
	}

	// Clean up the dashboards this controller copied into the client monitoring namespace
	// before dropping the finalizer. The monitoring namespace itself is left to its owner
	// (the operator ServiceAccount is not granted namespace deletion).
	monNs := clientorg.MonitoringNamespace(ns.Name)
	if err := r.kc.DeleteAllOf(ctx, &openvizapi.GrafanaDashboard{}, client.InNamespace(monNs)); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	if err := r.kc.DeleteAllOf(ctx, &openvizapi.PersesDashboard{}, client.InNamespace(monNs)); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	vt, err := cu.CreateOrPatch(context.TODO(), r.kc, &ns, func(in client.Object, createOp bool) client.Object {
		obj := in.(*core.Namespace)
		obj.ObjectMeta = core_util.RemoveFinalizer(obj.ObjectMeta, mona.PrometheusKey)

		return obj
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if vt != kutil.VerbUnchanged {
		klog.Infof("%s finalizer %s on Namespace %s", vt, mona.PrometheusKey, ns.Name)
	}
	return ctrl.Result{}, nil
}

// markCleanedUp stamps the cleaned-up marker on a "terminating" client-org namespace so a
// later reconcile of the still-present namespace skips the (already done) teardown.
func (r *ClientOrgReconciler) markCleanedUp(ctx context.Context, ns *core.Namespace) error {
	_, err := cu.Patch(ctx, r.kc, ns, func(in client.Object) client.Object {
		obj := in.(*core.Namespace)
		if obj.Annotations == nil {
			obj.Annotations = map[string]string{}
		}
		obj.Annotations[cleanedUpKey] = "true"

		return obj
	})
	return err
}

// ensureFinalizer adds the shared Prometheus finalizer to the namespace.
func (r *ClientOrgReconciler) ensureFinalizer(ctx context.Context, ns *core.Namespace) error {
	vt, err := cu.CreateOrPatch(ctx, r.kc, ns, func(in client.Object, createOp bool) client.Object {
		obj := in.(*core.Namespace)
		obj.ObjectMeta = core_util.AddFinalizer(obj.ObjectMeta, mona.PrometheusKey)

		return obj
	})
	if err != nil {
		return err
	}
	if vt != kutil.VerbUnchanged {
		klog.Infof("%s finalizer %s on Namespace %s", vt, mona.PrometheusKey, ns.Name)
	}
	return nil
}

// ensureMonitoringNamespace gets or creates the client's "{client}-monitoring" namespace.
// If that namespace is mid-teardown, it returns a non-zero RequeueAfter so the caller
// waits for it to fully delete before recreating it.
func (r *ClientOrgReconciler) ensureMonitoringNamespace(ctx context.Context, ns core.Namespace) (core.Namespace, ctrl.Result, error) {
	monNamespace := core.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: clientorg.MonitoringNamespace(ns.Name),
		},
	}
	switch err := r.kc.Get(ctx, client.ObjectKeyFromObject(&monNamespace), &monNamespace); {
	case apierrors.IsNotFound(err):
		if err := r.kc.Create(ctx, &monNamespace); err != nil {
			return monNamespace, ctrl.Result{}, err
		}
	case err != nil:
		return monNamespace, ctrl.Result{}, err
	case monNamespace.DeletionTimestamp != nil:
		// The monitoring namespace is being torn down (teardown deleted it, while a
		// stale-cache reconcile of the still-present client-org namespace re-entered
		// this live branch). Registering backends or copying dashboards into a
		// terminating namespace is rejected by admission and just flaps create/delete.
		// Wait for it to fully delete, then recreate it cleanly on a later reconcile.
		klog.Infof("monitoring namespace %s is terminating, requeueing", monNamespace.Name)
		return monNamespace, ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	return monNamespace, ctrl.Result{}, nil
}

// ensureMonitoringRoleBinding creates/patches the RoleBinding granting the client-org's
// group access to its monitoring namespace, used to generate grafana links.
func (r *ClientOrgReconciler) ensureMonitoringRoleBinding(ctx context.Context, monNamespace core.Namespace, clientOrgId string) error {
	rb := rbac.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crClientOrgMonitoring,
			Namespace: monNamespace.Name,
		},
	}
	rbvt, err := cu.CreateOrPatch(ctx, r.kc, &rb, func(in client.Object, createOp bool) client.Object {
		obj := in.(*rbac.RoleBinding)

		obj.RoleRef = rbac.RoleRef{
			APIGroup: rbac.GroupName,
			Kind:     "ClusterRole",
			Name:     crClientOrgMonitoring,
		}

		obj.Subjects = []rbac.Subject{
			{
				APIGroup: rbac.GroupName,
				Kind:     "Group",
				Name:     fmt.Sprintf("ace.org.%s", clientOrgId),
			},
		}

		return obj
	})
	if err != nil {
		return err
	}
	if rbvt != kutil.VerbUnchanged {
		klog.Infof("%s RoleBinding %s/%s", rbvt, rb.Namespace, rb.Name)
	}
	return nil
}
