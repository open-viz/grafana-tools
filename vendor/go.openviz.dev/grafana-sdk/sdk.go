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

package grafana_sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/go-resty/resty/v2"
	"gomodules.xyz/pointer"
	"k8s.io/apimachinery/pkg/runtime"
)

type Client struct {
	baseURL     string
	key         string
	isBasicAuth bool
	username    string
	password    string
	client      *resty.Client
}

type GrafanaDashboard struct {
	Dashboard *runtime.RawExtension `json:"dashboard,omitempty"`
	FolderId  int                   `json:"folderId,omitempty"`
	FolderUid string                `json:"FolderUid,omitempty"`
	Message   string                `json:"message,omitempty"`
	Overwrite bool                  `json:"overwrite,omitempty"`
}

type GrafanaResponse struct {
	ID      *int    `json:"id,omitempty"`
	UID     *string `json:"uid,omitempty"`
	URL     *string `json:"url,omitempty"`
	Title   *string `json:"title,omitempty"`
	Name    *string `json:"name,omitempty"`
	Message *string `json:"message,omitempty"`
	Status  *string `json:"status,omitempty"`
	Version *int    `json:"version,omitempty"`
	Slug    *string `json:"slug,omitempty"`
}

type Org struct {
	ID   *int    `json:"id,omitempty"`
	Name *string `json:"name,omitempty"`
}

type HealthResponse struct {
	Commit   string `json:"commit,omitempty"`
	Database string `json:"database,omitempty"`
	Version  string `json:"version,omitempty"`
}

// Datasource as described in the doc
// http://docs.grafana.org/reference/http_api/#get-all-datasources
type Datasource struct {
	ID                uint        `json:"id"`
	OrgID             uint        `json:"orgId"`
	Name              string      `json:"name"`
	Type              string      `json:"type"`
	Access            string      `json:"access"` // direct or proxy
	URL               string      `json:"url"`
	Password          *string     `json:"password,omitempty"`
	User              *string     `json:"user,omitempty"`
	Database          *string     `json:"database,omitempty"`
	BasicAuth         *bool       `json:"basicAuth,omitempty"`
	BasicAuthUser     *string     `json:"basicAuthUser,omitempty"`
	BasicAuthPassword *string     `json:"basicAuthPassword,omitempty"`
	IsDefault         bool        `json:"isDefault"`
	JSONData          interface{} `json:"jsonData"`
	SecureJSONData    interface{} `json:"secureJsonData"`
}

// NewClient initializes client for interacting with an instance of Grafana server;
// apiKeyOrBasicAuth accepts either 'username:password' basic authentication credentials,
// or a Grafana API key. If it is an empty string then no authentication is used.
func NewClient(hostURL string, keyOrBasicAuth string) (*Client, error) {
	isBasicAuth := strings.Contains(keyOrBasicAuth, ":")
	baseURL, err := url.Parse(hostURL)
	if err != nil {
		return nil, err
	}
	client := &Client{
		baseURL:     baseURL.String(),
		key:         "",
		isBasicAuth: isBasicAuth,
		username:    "",
		password:    "",
		client:      resty.New(),
	}
	if len(keyOrBasicAuth) > 0 {
		if !isBasicAuth {
			client.key = keyOrBasicAuth
		} else {
			auths := strings.Split(keyOrBasicAuth, ":")
			if len(auths) != 2 {
				return nil, errors.New("given basic auth format is invalid. expected format: <username>:<password>")
			}
			client.username = auths[0]
			client.password = auths[1]
		}
	}
	return client, nil
}

// SetDashboard will create or update grafana dashboard
func (c *Client) SetDashboard(ctx context.Context, db *GrafanaDashboard) (*GrafanaResponse, error) {
	u, _ := url.Parse(c.baseURL)
	u.Path = path.Join(u.Path, "api/dashboards/db")
	resp, err := c.do(ctx, http.MethodPost, u.String(), db)
	if err != nil {
		return nil, err
	}
	gResp := &GrafanaResponse{}
	err = json.Unmarshal(resp.Body(), gResp)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode() != http.StatusOK {
		return gResp, fmt.Errorf("failed to set dashboard, reason: %v", pointer.String(gResp.Message))
	}

	return gResp, nil
}

// DeleteDashboardByUID will delete the grafana dashboard with the given uid
func (c *Client) DeleteDashboardByUID(ctx context.Context, uid string) (*GrafanaResponse, error) {
	u, _ := url.Parse(c.baseURL)
	u.Path = path.Join(u.Path, fmt.Sprintf("api/dashboards/uid/%v", uid))
	resp, err := c.do(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return nil, err
	}
	gResp := &GrafanaResponse{}
	err = json.Unmarshal(resp.Body(), gResp)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode() != http.StatusOK {
		return gResp, fmt.Errorf("failed to delete dashboard, reason: %v", pointer.String(gResp.Message))
	}

	return gResp, nil
}

// GetCurrentOrg gets current organization.
// It reflects GET /api/org/ API call.
func (c *Client) GetCurrentOrg(ctx context.Context) (*Org, error) {
	u, _ := url.Parse(c.baseURL)
	u.Path = path.Join(u.Path, "api/org/")
	resp, err := c.do(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("failed to get current org, Status Code: %v", resp.StatusCode())
	}
	org := &Org{}
	err = json.Unmarshal(resp.Body(), org)
	if err != nil {
		return nil, err
	}
	return org, nil
}

// GetHealth returns the current health status
func (c *Client) GetHealth(ctx context.Context) (*HealthResponse, error) {
	u, _ := url.Parse(c.baseURL)
	u.Path = path.Join(u.Path, "api/health")
	resp, err := c.do(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	health := &HealthResponse{}
	err = json.Unmarshal(resp.Body(), health)
	if err != nil {
		return nil, err
	}
	return health, nil
}

func (c *Client) do(ctx context.Context, method string, url string, body interface{}) (*resty.Response, error) {
	req := c.client.R().SetContext(ctx).SetBody(body)
	if c.isBasicAuth {
		req = req.SetBasicAuth(c.username, c.password)
	} else {
		req = req.SetAuthToken(c.key)
	}

	var resp *resty.Response
	var err error
	switch method {
	case http.MethodGet:
		resp, err = req.Get(url)
	case http.MethodPost:
		resp, err = req.Post(url)
	case http.MethodDelete:
		resp, err = req.Delete(url)
	case http.MethodPut:
		resp, err = req.Put(url)
	default:
		return nil, errors.New("unsupported http method")
	}
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) CreateDatasource(ctx context.Context, ds *Datasource) (*GrafanaResponse, error) {
	u, _ := url.Parse(c.baseURL)
	u.Path = path.Join(u.Path, "api/datasources")
	resp, err := c.do(ctx, http.MethodPost, u.String(), ds)
	if err != nil {
		return nil, err
	}
	gResp := &GrafanaResponse{}
	err = json.Unmarshal(resp.Body(), gResp)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode() != http.StatusOK {
		return gResp, fmt.Errorf("failed to create datasource, reason: %v", pointer.String(gResp.Message))
	}

	return gResp, nil
}

func (c *Client) UpdateDatasource(ctx context.Context, ds Datasource) (*GrafanaResponse, error) {
	u, _ := url.Parse(c.baseURL)
	u.Path = path.Join(u.Path, fmt.Sprintf("api/datasources/%v", ds.ID))
	resp, err := c.do(ctx, http.MethodPut, u.String(), ds)
	if err != nil {
		return nil, err
	}
	gResp := &GrafanaResponse{}
	err = json.Unmarshal(resp.Body(), gResp)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode() != http.StatusOK {
		return gResp, fmt.Errorf("failed to update datasource, reason: %v", pointer.String(gResp.Message))
	}

	return gResp, nil
}

func (c *Client) DeleteDatasource(ctx context.Context, id int) (*GrafanaResponse, error) {
	u, _ := url.Parse(c.baseURL)
	u.Path = path.Join(u.Path, fmt.Sprintf("api/datasources/%v", id))
	resp, err := c.do(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return nil, err
	}
	gResp := &GrafanaResponse{}
	err = json.Unmarshal(resp.Body(), gResp)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode() != http.StatusOK {
		return gResp, fmt.Errorf("failed to delete datasource, reason: %v", pointer.String(gResp.Message))
	}

	return gResp, nil
}
