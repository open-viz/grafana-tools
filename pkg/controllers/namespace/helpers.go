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

func isActiveClientOrg(ns *core.Namespace) bool {
	return ns.DeletionTimestamp == nil &&
		ns.Labels[kmapi.ClientOrgKey] != "" &&
		ns.Labels[kmapi.ClientOrgKey] != "terminating"
}

func (r *ClientOrgReconciler) handleDeletion(ctx context.Context, ns core.Namespace, clientOrgId string) (ctrl.Result, error) {
	// Unregister is best-effort: a hub failure here must not block teardown, otherwise the
	// namespace stays stuck in terminating forever whenever the backend is unreachable. Log
	// and continue so the local cleanup and finalizer removal below always run.
	if r.pc != nil {
		if err := r.pc.Unregister(mona.PrometheusContext{
			HubUID:      r.hubUID,
			ClusterUID:  r.clusterUID,
			ProjectId:   "",
			Default:     false,
			IssueToken:  true,
			ClientOrgID: clientOrgId,
		}); err != nil {
			klog.Errorf("failed to unregister grafana backend for client-org %s: %v", clientOrgId, err)
		}

		if err := r.pc.UnregisterPerses(mona.PrometheusContext{
			HubUID:      r.hubUID,
			ClusterUID:  r.clusterUID,
			ProjectId:   "",
			Default:     false,
			IssueToken:  true,
			ClientOrgID: clientOrgId,
		}); err != nil {
			klog.Errorf("failed to unregister perses backend for client-org %s: %v", clientOrgId, err)
		}
		klog.Infof("unregister attempt for grafana/perses backends done for client-org %s", clientOrgId)
	}

	// The monitoring namespace itself is left to its owner; the operator ServiceAccount is not
	// granted namespace deletion.
	monNs := clientorg.MonitoringNamespace(ns.Name)
	if err := r.kc.DeleteAllOf(ctx, &openvizapi.GrafanaDashboard{}, client.InNamespace(monNs)); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	if err := r.kc.DeleteAllOf(ctx, &openvizapi.PersesDashboard{}, client.InNamespace(monNs)); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	vt, err := cu.Patch(context.TODO(), r.kc, &ns, func(in client.Object) client.Object {
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

func (r *ClientOrgReconciler) ensureFinalizer(ctx context.Context, ns *core.Namespace) error {
	vt, err := cu.Patch(ctx, r.kc, ns, func(in client.Object) client.Object {
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
		// Registering backends or copying dashboards into a terminating namespace is rejected
		// by admission and just flaps create/delete; wait for it to fully delete first.
		klog.Infof("monitoring namespace %s is terminating, requeueing", monNamespace.Name)
		return monNamespace, ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	return monNamespace, ctrl.Result{}, nil
}

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
