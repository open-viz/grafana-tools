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

func (f *Framework) GetGrafanaDashboard() (*api.GrafanaDashboard, error) {
	return f.extClient.OpenvizV1alpha1().GrafanaDashboards(f.namespace).Get(context.TODO(), f.name, metav1.GetOptions{})
}

func (f *Framework) CreateGrafanaDashboard(grafanadashboard *api.GrafanaDashboard) error {
	_, err := f.extClient.OpenvizV1alpha1().GrafanaDashboards(grafanadashboard.Namespace).Create(context.TODO(), grafanadashboard, metav1.CreateOptions{})
	return err
}

func (f *Framework) DeleteGrafanaDashboard() error {
	return f.extClient.OpenvizV1alpha1().GrafanaDashboards(f.namespace).Delete(context.TODO(), f.name, metav1.DeleteOptions{})
}
