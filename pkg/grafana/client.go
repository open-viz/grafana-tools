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
	"crypto/tls"
	"net"
	"net/url"

	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"
	sdk "go.openviz.dev/grafana-sdk"

	"github.com/go-resty/resty/v2"
	core "k8s.io/api/core/v1"
	kmapi "kmodules.xyz/client-go/api/v1"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewGrafanaClient(ctx context.Context, kc client.Client, ref *kmapi.ObjectReference) (*sdk.Client, error) {
	ab, err := openvizapi.GetGrafana(ctx, kc, ref)
	if err != nil {
		return nil, err
	}

	return newGrafanaClient(ctx, kc, ab)
}

func newGrafanaClient(ctx context.Context, kc client.Client, ab *appcatalog.AppBinding) (*sdk.Client, error) {
	var authSecret *core.Secret
	if ab.Spec.Secret != nil && ab.Spec.Secret.Name != "" {
		var sec core.Secret
		if err := kc.Get(ctx, client.ObjectKey{Namespace: ab.Namespace, Name: ab.Spec.Secret.Name}, &sec); err != nil {
			return nil, err
		}
		authSecret = &sec
	}

	cfg, err := GetGrafanaConfig(ab, authSecret)
	if err != nil {
		return nil, err
	}
	httpClient := resty.New()
	if cfg.TLS != nil && len(cfg.TLS.CABundle) > 0 {
		httpClient.SetRootCertificateFromString(string(cfg.TLS.CABundle))
	}
	u, err := url.Parse(cfg.Addr)
	if err != nil {
		return nil, err
	}
	// use InsecureSkipVerify, if IP address is used for baseURL host
	if ip := net.ParseIP(u.Hostname()); ip != nil && u.Scheme == "https" {
		httpClient.SetTLSClientConfig(&tls.Config{InsecureSkipVerify: true})
	}

	return sdk.NewClient(cfg.Addr, cfg.AuthConfig, httpClient)
}

func GetGrafanaConfig(ab *appcatalog.AppBinding, authSecret *core.Secret) (*Config, error) {
	addr, err := ab.URL()
	if err != nil {
		return nil, err
	}

	var auth *sdk.AuthConfig
	if authSecret != nil {
		if token, ok := authSecret.Data["token"]; ok {
			auth = &sdk.AuthConfig{
				BearerToken: string(token),
			}
		} else {
			auth = &sdk.AuthConfig{
				BasicAuth: &sdk.BasicAuth{},
			}
			if v, ok := authSecret.Data[core.BasicAuthUsernameKey]; ok {
				auth.BasicAuth.Username = string(v)
			}
			if v, ok := authSecret.Data[core.BasicAuthPasswordKey]; ok {
				auth.BasicAuth.Password = string(v)
			}
		}
	}
	cfg := &Config{
		Addr:       addr,
		AuthConfig: auth,
	}
	if len(ab.Spec.ClientConfig.CABundle) > 0 {
		cfg.TLS = &TLS{
			CABundle: ab.Spec.ClientConfig.CABundle,
		}
	}

	return cfg, nil
}
