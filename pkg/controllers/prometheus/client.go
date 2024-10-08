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
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"

	"go.openviz.dev/apimachinery/apis/openviz"
	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"

	"go.bytebuilders.dev/license-verifier/info"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/klog/v2"
	mona "kmodules.xyz/monitoring-agent-api/api/v1"
	"moul.io/http2curl/v2"
)

type GrafanaDatasourceResponse struct {
	Grafana             mona.GrafanaConfig `json:"grafana"`
	mona.GrafanaContext `json:",inline,omitempty"`
}

type Client struct {
	baseURL string
	token   string
	caCert  []byte
	client  *http.Client
}

func NewClient(baseURL, token string, caCert []byte) (*Client, error) {
	c := &Client{
		baseURL: baseURL,
		token:   token,
		caCert:  caCert,
	}
	if len(caCert) == 0 {
		u, err := url.Parse(baseURL)
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
		caCertPool.AppendCertsFromPEM(caCert)

		tlsConfig := &tls.Config{
			RootCAs: caCertPool,
		}
		transport := &http.Transport{TLSClientConfig: tlsConfig}
		c.client = &http.Client{Transport: transport}
	}
	return c, nil
}

const (
	registerAPIPath   = "api/v1/trickster/register"
	unregisterAPIPath = "api/v1/trickster/unregister"
)

func (c *Client) registerAPIEndpoint() (string, error) {
	u, err := info.APIServerAddress(c.baseURL)
	if err != nil {
		return "", err
	}
	u.Path = path.Join(u.Path, registerAPIPath)
	return u.String(), nil
}

func (c *Client) unregisterAPIEndpoint() (string, error) {
	u, err := info.APIServerAddress(c.baseURL)
	if err != nil {
		return "", err
	}
	u.Path = path.Join(u.Path, unregisterAPIPath)
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

	url, err := c.registerAPIEndpoint()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// add authorization header to the req
	if c.token != "" {
		req.Header.Add("Authorization", "Bearer "+c.token)
	}
	if klog.V(8).Enabled() {
		command, _ := http2curl.GetCurlCommand(req)
		klog.V(8).Infoln(command.String())
	}

	resp, err := c.client.Do(req)
	if err != nil {
		var ce *tls.CertificateVerificationError
		if errors.As(err, &ce) {
			klog.ErrorS(err, "UnverifiedCertificates")
			for _, cert := range ce.UnverifiedCertificates {
				klog.Errorln(string(encodeCertPEM(cert)))
			}
		}
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

	url, err := c.unregisterAPIEndpoint()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// add authorization header to the req
	if c.token != "" {
		req.Header.Add("Authorization", "Bearer "+c.token)
	}
	if klog.V(8).Enabled() {
		command, _ := http2curl.GetCurlCommand(req)
		klog.V(8).Infoln(command.String())
	}

	resp, err := c.client.Do(req)
	if err != nil {
		var ce *tls.CertificateVerificationError
		if errors.As(err, &ce) {
			klog.ErrorS(err, "UnverifiedCertificates")
			for _, cert := range ce.UnverifiedCertificates {
				klog.Errorln(string(encodeCertPEM(cert)))
			}
		}
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

func (c *Client) CACert() []byte {
	return c.caCert
}

func encodeCertPEM(cert *x509.Certificate) []byte {
	block := pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	}
	return pem.EncodeToMemory(&block)
}
