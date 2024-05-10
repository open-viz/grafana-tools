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

package grafana

import (
	"context"
	"encoding/json"
	"sync"

	sdk "go.openviz.dev/grafana-sdk"

	"github.com/go-resty/resty/v2"
	"github.com/zeebo/xxh3"
	core "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type ClientBuilder struct {
	mgr manager.Manager

	mu sync.Mutex

	flags    *Config
	existing uint64
	appCfg   *Config

	// last cfg
	cfg *Config
	c   *sdk.Client
}

func NewBuilder(mgr manager.Manager, flags *Config) (*ClientBuilder, error) {
	return &ClientBuilder{
		mgr:      mgr,
		flags:    flags,
		existing: 0,
	}, nil
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *ClientBuilder) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	key := req.NamespacedName

	app := &appcatalog.AppBinding{}
	if err := r.mgr.GetClient().Get(ctx, key, app); err != nil {
		klog.Infof("AppBinding %q doesn't exist anymore", req.NamespacedName.String())
		r.unset()
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Add or remove finalizer based on deletion timestamp
	if app.ObjectMeta.DeletionTimestamp != nil {
		r.unset()
		return ctrl.Result{}, nil
	}

	cfg, err := r.build(app)
	if err != nil {
		r.unset()
		return ctrl.Result{}, err
	}

	data, _ := json.Marshal(cfg)
	nuHash := xxh3.Hash(data)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.existing != nuHash {
		r.appCfg = cfg
		r.existing = nuHash
	}
	return ctrl.Result{}, nil
}

func (r *ClientBuilder) Setup() error {
	if err := r.mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&appcatalog.AppBinding{},
		mona.DefaultGrafanaKey,
		func(rawObj client.Object) []string {
			app := rawObj.(*appcatalog.AppBinding)
			if v, ok := app.Annotations[mona.DefaultGrafanaKey]; ok && v == "true" {
				return []string{"true"}
			}
			return nil
		}); err != nil {
		return err
	}

	authHandler := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []reconcile.Request {
		var appList appcatalog.AppBindingList
		err := r.mgr.GetClient().List(ctx, &appList, client.MatchingFields{
			mona.DefaultGrafanaKey: "true",
		})
		if err != nil {
			return nil
		}

		var req []reconcile.Request
		for _, app := range appList.Items {
			if app.GetNamespace() == a.GetNamespace() &&
				(app.Spec.Secret != nil && app.Spec.Secret.Name == a.GetName() ||
					app.Spec.TLSSecret != nil && app.Spec.TLSSecret.Name == a.GetName()) {
				req = append(req, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&app)})
			}
		}
		return req
	})

	return ctrl.NewControllerManagedBy(r.mgr).
		For(&appcatalog.AppBinding{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			if v, ok := obj.GetAnnotations()[mona.DefaultGrafanaKey]; ok {
				return v == "true"
			}
			return false
		}))).
		Watches(&core.Secret{}, authHandler).
		Complete(r)
}

func (r *ClientBuilder) build(app *appcatalog.AppBinding) (*Config, error) {
	var authSecret *core.Secret
	key := client.ObjectKey{Namespace: app.Namespace, Name: app.Spec.Secret.Name}
	if app.Spec.Secret != nil && app.Spec.Secret.Name != "" {
		var sec core.Secret
		if err := r.mgr.GetClient().Get(context.TODO(), key, &sec); err != nil {
			return nil, err
		}
		authSecret = &sec
	}

	return GetGrafanaConfig(app, authSecret)
}

func (r *ClientBuilder) unset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appCfg = nil
	r.existing = 0
}

type ChangeStats struct {
	Comment string `json:"comment,omitempty"`
	Diff    string `json:"diff,omitempty"`
}

func (r *ClientBuilder) GetGrafanaClient() (*sdk.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var cfg *Config
	if r.appCfg != nil {
		cfg = r.appCfg
	} else if r.flags != nil && r.flags.Addr != "" {
		cfg = r.flags
	}
	if cfg == nil {
		return nil, nil
	}
	if r.cfg == cfg { // pointer equality
		return r.c, nil
	}
	httpClient := resty.New()
	if cfg.TLS != nil && len(cfg.TLS.CABundle) > 0 {
		httpClient.SetRootCertificateFromString(string(cfg.TLS.CABundle))
	}

	gc, err := sdk.NewClient(r.cfg.Addr, r.cfg.AuthConfig, httpClient)
	if err != nil {
		return nil, err
	}
	r.cfg = cfg
	r.c = gc
	return r.c, nil
}
