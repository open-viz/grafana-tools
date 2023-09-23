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

package prometheus

import (
	"bytes"
	"io"
	"net/http"
	"path"

	"go.openviz.dev/apimachinery/apis/openviz"
	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"

	"go.bytebuilders.dev/license-verifier/info"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
)

type GrafanaDatasourceResponse struct {
	Grafana             mona.GrafanaConfig `json:"grafana"`
	mona.GrafanaContext `json:",inline,omitempty"`
}

type Client struct {
	url   string
	token string
}

func NewClient(baseURL, token string) (*Client, error) {
	u, err := registerAPIEndpoint(baseURL)
	if err != nil {
		return nil, err
	}
	return &Client{
		url:   u,
		token: token,
	}, nil
}

const (
	registerAPIPath = "api/v1/FIX_IT____________"
)

func registerAPIEndpoint(override ...string) (string, error) {
	u, err := info.APIServerAddress(override...)
	if err != nil {
		return "", err
	}
	u.Path = path.Join(u.Path, registerAPIPath)
	return u.String(), nil
}

func (c *Client) Register(ctx mona.PrometheusContext, cfg mona.PrometheusConfig) (*GrafanaDatasourceResponse, error) {
	opts := struct {
		mona.PrometheusContext `json:",inline,omitempty"`
		Prometheus             mona.PrometheusConfig `json:"prometheus"`
	}{
		PrometheusContext: ctx,
		Prometheus:        cfg,
	}
	data, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// add authorization header to the req
	if c.token != "" {
		req.Header.Add("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, apierrors.NewGenericServerResponse(
			resp.StatusCode,
			http.MethodPost,
			schema.GroupResource{Group: openviz.GroupName, Resource: openvizapi.ResourceGrafanaDatasources},
			"",
			string(body),
			0,
			false,
		)
	}

	var ds GrafanaDatasourceResponse
	err = json.Unmarshal(body, &ds)
	if err != nil {
		return nil, err
	}
	return &ds, nil
}

func (c *Client) Unregister(ctx mona.PrometheusContext) error {
	data, err := json.Marshal(ctx)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodDelete, c.url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// add authorization header to the req
	if c.token != "" {
		req.Header.Add("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return apierrors.NewGenericServerResponse(
			resp.StatusCode,
			http.MethodDelete,
			schema.GroupResource{Group: openviz.GroupName, Resource: openvizapi.ResourceGrafanaDatasources},
			"",
			"",
			0,
			false,
		)
	}
	return nil
}
