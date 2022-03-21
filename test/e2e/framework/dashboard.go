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

package framework

import (
	"context"
	"errors"
	"time"

	api "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"

	"gomodules.xyz/wait"
	kmc "kmodules.xyz/client-go/client"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	interval = time.Second * 5
	timeout  = time.Minute * 5
)

func (f *Framework) GetGrafanaDashboard() (*api.GrafanaDashboard, error) {
	db := &api.GrafanaDashboard{}
	if err := f.cc.Get(context.TODO(), client.ObjectKey{Namespace: f.namespace, Name: f.name}, db); err != nil {
		return nil, err
	}
	return db, nil
}

func (f *Framework) CreateOrUpdateGrafanaDashboard(db *api.GrafanaDashboard) error {
	_, _, err := kmc.CreateOrPatch(context.TODO(), f.cc, db, func(obj client.Object, createOp bool) client.Object {
		return db
	})
	return err
}

func (f *Framework) DeleteGrafanaDashboard(db *api.GrafanaDashboard) error {
	return f.cc.Delete(context.TODO(), db)
}

func (f *Framework) WaitForGrafanaPhaseToBeCurrent() error {
	return wait.PollImmediate(interval, timeout, func() (bool, error) {
		gdb, err := f.GetGrafanaDashboard()
		if err != nil {
			return false, nil
		}
		if gdb.Status.Phase != api.GrafanaPhaseCurrent {
			return false, nil
		}
		if gdb.Status.Dashboard.ID == nil {
			return false, errors.New("dashboard ID should not be nil")
		}
		if gdb.Status.Dashboard.UID == nil {
			return false, errors.New("dashboard UID should not be nil")
		}
		return true, nil
	})
}
