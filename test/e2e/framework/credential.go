/*
Copyright The Searchlight Authors.

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
	"fmt"
	"net/url"
	"strconv"

	"github.com/appscode/go/types"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	appcat "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
)

func (f *Framework) AppBindingName() string {
	return f.name
}

func (f *Framework) CreateAppBinding() error {
	obj := &appcat.AppBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      f.name,
			Namespace: f.namespace,
		},
		Spec: appcat.AppBindingSpec{
			Secret: &core.LocalObjectReference{
				Name: f.name,
			},
			ClientConfig: appcat.ClientConfig{
				//URL: types.StringP(apiURL),
				Service: &appcat.ServiceReference{
					Scheme: "http",
					Name:   f.name,
					Port:   3000,
				},
			},
		},
	}

	_, err := f.appcatClient.AppBindings(obj.Namespace).Create(obj)
	return err
}

func (f *Framework) DeleteAppBinding() error {
	return f.appcatClient.AppBindings(f.namespace).Delete(f.name, nil)
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
	_, err := f.kubeClient.CoreV1().Secrets(obj.Namespace).Create(obj)
	return err
}

func (f *Framework) DeleteSecret() error {
	return f.kubeClient.CoreV1().Secrets(f.namespace).Delete(f.name, nil)
}

// getApiURLandApiKey extracts ApiURL and ApiKey from appBinding and secret
func (f *Framework) getApiURLandApiKey(appBinding *v1alpha1.AppBinding, secret *core.Secret) (apiURL, apiKey string, err error) {
	cfg := appBinding.Spec.ClientConfig
	if cfg.URL != nil {
		apiURL = types.String(cfg.URL)
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
