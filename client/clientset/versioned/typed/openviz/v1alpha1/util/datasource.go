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
	"fmt"

	api "go.openviz.dev/grafana-operator/apis/openviz/v1alpha1"
	cs "go.openviz.dev/grafana-operator/client/clientset/versioned/typed/openviz/v1alpha1"

	jsonpatch "github.com/evanphx/json-patch"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	kutil "kmodules.xyz/client-go"
)

func PatchDatasource(
	ctx context.Context,
	c cs.OpenvizV1alpha1Interface,
	cur *api.Datasource,
	transform func(*api.Datasource) *api.Datasource,
	opts metav1.PatchOptions,
) (*api.Datasource, kutil.VerbType, error) {
	return PatchDatasourceObject(ctx, c, cur, transform(cur.DeepCopy()), opts)
}

func PatchDatasourceObject(
	ctx context.Context,
	c cs.OpenvizV1alpha1Interface,
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
	klog.V(3).Infof("Patching Datasource %s/%s with %s.", cur.Namespace, cur.Name, string(patch))
	out, err := c.Datasources(cur.Namespace).Patch(ctx, cur.Name, types.MergePatchType, patch, opts)
	return out, kutil.VerbPatched, err
}

func UpdateDatasourceStatus(
	ctx context.Context,
	c cs.OpenvizV1alpha1Interface,
	meta metav1.ObjectMeta,
	transform func(*api.DatasourceStatus) *api.DatasourceStatus,
	opts metav1.UpdateOptions,
) (result *api.Datasource, err error) {
	apply := func(x *api.Datasource) *api.Datasource {
		return &api.Datasource{
			TypeMeta:   x.TypeMeta,
			ObjectMeta: x.ObjectMeta,
			Spec:       x.Spec,
			Status:     *transform(x.Status.DeepCopy()),
		}
	}

	attempt := 0
	cur, err := c.Datasources(meta.Namespace).Get(ctx, meta.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	err = wait.PollImmediate(kutil.RetryInterval, kutil.RetryTimeout, func() (bool, error) {
		attempt++
		var e2 error
		result, e2 = c.Datasources(meta.Namespace).UpdateStatus(ctx, apply(cur), opts)
		if kerr.IsConflict(e2) {
			latest, e3 := c.Datasources(meta.Namespace).Get(ctx, meta.Name, metav1.GetOptions{})
			switch {
			case e3 == nil:
				cur = latest
				return false, nil
			case kutil.IsRequestRetryable(e3):
				return false, nil
			default:
				return false, e3
			}
		} else if err != nil && !kutil.IsRequestRetryable(e2) {
			return false, e2
		}
		return e2 == nil, nil
	})

	if err != nil {
		err = fmt.Errorf("failed to update status of Dashboard %s/%s after %d attempts due to %v", meta.Namespace, meta.Name, attempt, err)
	}
	return
}
