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
	"fmt"
	"time"

	api "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"

	. "github.com/onsi/gomega"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
)

func (f *Framework) EventuallyCRD() GomegaAsyncAssertion {
	return Eventually(
		func() error {
			// Check GrafanaDashboard CRD
			if err := f.cc.List(context.TODO(), &api.GrafanaDashboardList{}); err != nil {
				return fmt.Errorf("CRD GrafanaDashboard is not ready. Reason: %v", err)
			}

			// Check AppBinding CRD
			if err := f.cc.List(context.TODO(), &appcatalog.AppBindingList{}); err != nil {
				return fmt.Errorf("CRD AppBinding is not ready. Reason: %v", err)
			}

			//// Check GrafanaDatasource CRD
			//if err := f.cc.List(context.TODO(), &api.GrafanaDatasourceList{}); err != nil {
			//	return fmt.Errorf("CRD GrafanaDatasource is not ready. Reason: %v", err)
			//}
			return nil
		},
		time.Minute*2,
		time.Second*10,
	)
}
