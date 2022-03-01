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
	"fmt"
	"net/url"
	"strconv"

	api "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"

	"gomodules.xyz/pointer"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	appcatalogapi "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
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
					Scheme: "http",
					Name:   f.name,
					Port:   3000,
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
		api.DefaultGrafanaKey: "true",
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

func (f *Framework) CreateSecret(apiKey string) error {
	obj := &core.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      f.name,
			Namespace: f.namespace,
		},
		Type: core.SecretTypeOpaque,
		Data: map[string][]byte{
			"apiKey": []byte(apiKey),
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

// getApiURLandApiKey extracts ApiURL and ApiKey from appBinding and secret
func (f *Framework) getApiURLandApiKey(appBinding *v1alpha1.AppBinding, secret *core.Secret) (apiURL, apiKey string, err error) {
	cfg := appBinding.Spec.ClientConfig
	if cfg.URL != nil {
		apiURL = pointer.String(cfg.URL)
	} else if cfg.Service != nil {
		apiurl := url.URL{
			Scheme:   cfg.Service.Scheme,
			Host:     cfg.Service.Name + "." + appBinding.Namespace + "." + "svc" + ":" + strconv.Itoa(int(cfg.Service.Port)),
			Path:     cfg.Service.Path,
			RawQuery: cfg.Service.Query,
		}
		apiURL = apiurl.String()
	}
	apiKey = string(secret.Data["apiKey"])
	if len(apiURL) == 0 || len(apiKey) == 0 {
		err = fmt.Errorf("apiURL or apiKey not provided")
	}
	return
}
