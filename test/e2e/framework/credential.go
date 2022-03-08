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
	"encoding/json"

	api "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	appcatalogapi "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
)

func (f *Framework) AppBindingName() string {
	return f.name
}

func (f *Framework) AppBindingNamespace() string {
	return f.namespace
}

func (f *Framework) getAppBinding() (*appcatalogapi.AppBinding, error) {
	dsConfig := &api.GrafanaConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "GrafanaConfiguration",
			APIVersion: "openviz.dev/v1alpha1",
		},
		Datasource: "some-datasource",
		FolderID:   nil,
	}
	byteData, err := json.Marshal(dsConfig)
	if err != nil {
		return nil, err
	}
	obj := &appcatalogapi.AppBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      f.name,
			Namespace: f.namespace,
		},
		Spec: appcatalogapi.AppBindingSpec{
			Secret: &core.LocalObjectReference{
				Name: f.name,
			},
			ClientConfig: appcatalogapi.ClientConfig{
				// URL: pointer.StringP(apiURL),
				Service: &appcatalogapi.ServiceReference{
					Scheme:    "http",
					Name:      f.name,
					Namespace: f.namespace,
					Port:      3000,
				},
			},
			Parameters: &runtime.RawExtension{
				Raw: byteData,
			},
		},
	}
	return obj, nil
}

func (f *Framework) CreateAppBinding() error {
	ab, err := f.getAppBinding()
	if err != nil {
		return err
	}
	return f.cc.Create(context.TODO(), ab)
}

func (f *Framework) CreateDefaultAppBinding() error {
	ab, err := f.getAppBinding()
	if err != nil {
		return err
	}
	ab.ObjectMeta.Annotations = map[string]string{
		mona.DefaultGrafanaKey: "true",
	}
	return f.cc.Create(context.TODO(), ab)
}

func (f *Framework) DeleteAppBinding() error {
	return f.cc.Delete(context.TODO(), &appcatalogapi.AppBinding{ObjectMeta: metav1.ObjectMeta{
		Name:      f.name,
		Namespace: f.namespace,
	}})
}

func (f *Framework) SecretName() string {
	return f.name
}

func (f *Framework) CreateSecret(username, password string) error {
	obj := &core.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      f.name,
			Namespace: f.namespace,
		},
		Type: core.SecretTypeOpaque,
		Data: map[string][]byte{
			core.BasicAuthUsernameKey: []byte(username),
			core.BasicAuthPasswordKey: []byte(password),
		},
	}
	return f.cc.Create(context.TODO(), obj)
}

func (f *Framework) DeleteSecret() error {
	return f.cc.Delete(context.TODO(), &core.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      f.name,
		Namespace: f.namespace,
	}})
}
