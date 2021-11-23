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

	api "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (f *Framework) GetDashboard() (*api.Dashboard, error) {
	return f.extClient.OpenvizV1alpha1().Dashboards(f.namespace).Get(context.TODO(), f.name, metav1.GetOptions{})
}

func (f *Framework) CreateDashboard(dashboard *api.Dashboard) error {
	_, err := f.extClient.OpenvizV1alpha1().Dashboards(dashboard.Namespace).Create(context.TODO(), dashboard, metav1.CreateOptions{})
	return err
}

func (f *Framework) DeleteDashboard() error {
	return f.extClient.OpenvizV1alpha1().Dashboards(f.namespace).Delete(context.TODO(), f.name, metav1.DeleteOptions{})
}
