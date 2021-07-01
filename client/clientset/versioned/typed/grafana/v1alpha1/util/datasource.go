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

package util

import (
	"context"
	"encoding/json"

	api "go.searchlight.dev/grafana-operator/apis/grafana/v1alpha1"
	cs "go.searchlight.dev/grafana-operator/client/clientset/versioned/typed/grafana/v1alpha1"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/golang/glog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kutil "kmodules.xyz/client-go"
)

func PatchDatasource(
	ctx context.Context,
	c cs.GrafanaV1alpha1Interface,
	cur *api.Datasource,
	transform func(*api.Datasource) *api.Datasource,
	opts metav1.PatchOptions,
) (*api.Datasource, kutil.VerbType, error) {
	return PatchDatasourceObject(ctx, c, cur, transform(cur.DeepCopy()), opts)
}

func PatchDatasourceObject(
	ctx context.Context,
	c cs.GrafanaV1alpha1Interface,
	cur, mod *api.Datasource,
	opts metav1.PatchOptions,
) (*api.Datasource, kutil.VerbType, error) {
	curJson, err := json.Marshal(cur)
	if err != nil {
		return nil, kutil.VerbUnchanged, err
	}

	modJson, err := json.Marshal(mod)
	if err != nil {
		return nil, kutil.VerbUnchanged, err
	}

	patch, err := jsonpatch.CreateMergePatch(curJson, modJson)
	if err != nil {
		return nil, kutil.VerbUnchanged, err
	}
	if len(patch) == 0 || string(patch) == "{}" {
		return cur, kutil.VerbUnchanged, nil
	}
	glog.V(3).Infof("Patching Datasource %s/%s with %s.", cur.Namespace, cur.Name, string(patch))
	out, err := c.Datasources(cur.Namespace).Patch(ctx, cur.Name, types.MergePatchType, patch, opts)
	return out, kutil.VerbPatched, err
}
