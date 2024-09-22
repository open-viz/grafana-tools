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

package ranchertoken

import (
	"context"
	"time"

	"go.openviz.dev/grafana-tools/pkg/rancherutil"

	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clustermeta "kmodules.xyz/client-go/cluster"
	meta_util "kmodules.xyz/client-go/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	renewalOffset = 24 * time.Hour
)

var selfNamespace = meta_util.PodNamespace()

// TokenRefresher refreshes rancher token 24 hrs before expiration
type TokenRefresher struct {
	kc                    client.Client
	scheme                *runtime.Scheme
	rancherAuthSecretName string
}

func NewTokenRefresher(kc client.Client, rancherAuthSecretName string) *TokenRefresher {
	return &TokenRefresher{
		kc:                    kc,
		scheme:                kc.Scheme(),
		rancherAuthSecretName: rancherAuthSecretName,
	}
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Prometheus object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.0/pkg/reconcile
func (r *TokenRefresher) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var sec core.Secret
	if err := r.kc.Get(ctx, req.NamespacedName, &sec); err != nil {
		log.Error(err, "unable to fetch Secret")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	d, err := r.processToken(ctx, &sec)
	return ctrl.Result{RequeueAfter: d}, err
}

func (r *TokenRefresher) processToken(ctx context.Context, sec *core.Secret) (time.Duration, error) {
	cm, err := clustermeta.ClusterMetadata(r.kc)
	if err != nil {
		return 0, err
	}
	state := cm.State()

	rc, token, err := rancherutil.FindToken(ctx, sec, state)
	if err != nil {
		return 0, err
	}

	if time.Until(token.ExpiresAt) < renewalOffset {
		newToken, err := rancherutil.RenewToken(ctx, rc, r.kc, sec, state)
		if err != nil {
			return 0, err
		}
		return time.Duration(newToken.TTLMillis)*time.Millisecond - renewalOffset, nil
	}
	return time.Until(token.ExpiresAt) - renewalOffset, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TokenRefresher) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&core.Secret{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetNamespace() == selfNamespace && obj.GetName() == r.rancherAuthSecretName
		}))).
		Complete(r)
}
