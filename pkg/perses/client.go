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

package perses

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	sdk "go.openviz.dev/grafana-sdk"

	"github.com/pkg/errors"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kmapi "kmodules.xyz/client-go/api/v1"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Client struct {
	baseURL string
	token   string
	caCert  []byte
	client  *http.Client
}

func NewPersesClient(ctx context.Context, kc client.Client, ref *kmapi.ObjectReference) (*Client, error) {
	ab, err := GetPerses(ctx, kc, ref)
	if err != nil {
		return nil, err
	}

	return newPersesClient(ctx, kc, ab)
}

func GetPerses(ctx context.Context, kc client.Client, ref *kmapi.ObjectReference) (*appcatalog.AppBinding, error) {
	if ref != nil {
		var perses appcatalog.AppBinding
		err := kc.Get(ctx, ref.ObjectKey(), &perses)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to fetch AppBinding %s", ref.ObjectKey())
		}
		return &perses, nil
	}

	var persesList appcatalog.AppBindingList
	// any namespace, default Grafana AppBinding
	if err := kc.List(ctx, &persesList, client.MatchingFields{
		mona.DefaultPersesKey: "true",
	}); err != nil {
		return nil, err
	}
	if len(persesList.Items) == 0 {
		return nil, apierrors.NewNotFound(appcatalog.Resource(appcatalog.ResourceKindApp), "no default Perses appbinding found.")
	} else if len(persesList.Items) > 1 {
		names := make([]string, len(persesList.Items))
		for idx, item := range persesList.Items {
			names[idx] = fmt.Sprintf("%s/%s", item.Namespace, item.Name)
		}
		return nil, apierrors.NewBadRequest(fmt.Sprintf("multiple Perses appbindings %s are marked default", strings.Join(names, ",")))
	}
	return &persesList.Items[0], nil
}

func newPersesClient(ctx context.Context, kc client.Client, ab *appcatalog.AppBinding) (*Client, error) {
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
	c := &Client{
		baseURL: cfg.Addr,
		token:   cfg.AuthConfig.BearerToken,
		caCert:  cfg.TLS.CABundle,
	}
	if len(c.caCert) == 0 {
		u, err := url.Parse(c.baseURL)
		if err != nil {
			return nil, err
		}
		// use InsecureSkipVerify, if IP address is used for baseURL host
		if ip := net.ParseIP(u.Hostname()); ip != nil && u.Scheme == "https" {
			customTransport := http.DefaultTransport.(*http.Transport).Clone()
			customTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
			c.client = &http.Client{Transport: customTransport}
		} else {
			c.client = http.DefaultClient
		}
	} else {
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(c.caCert)

		tlsConfig := &tls.Config{
			RootCAs: caCertPool,
		}
		transport := &http.Transport{TLSClientConfig: tlsConfig}
		c.client = &http.Client{Transport: transport}
	}
	return c, nil
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
