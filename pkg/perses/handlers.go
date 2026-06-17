package perses

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"

	v1 "github.com/perses/perses/pkg/model/api/v1"
	"go.bytebuilders.dev/license-verifier/info"
	"k8s.io/klog/v2"
	"moul.io/http2curl/v2"
)

func (c *Client) deleteDashboardByNameAPIEndpoint(projectName, folderName, dashboardName string) (string, error) {
	u, err := info.APIServerAddress(c.baseURL)
	if err != nil {
		return "", err
	}
	deletePath := fmt.Sprintf("api/v1/projects/%s/folders/%s/dashboards/%s", projectName, folderName, dashboardName)
	u.Path = path.Join(u.Path, deletePath)
	return u.String(), nil
}

func (c *Client) createDashboardAPIEndpoint(projectName, folderName string) (string, error) {
	u, err := info.APIServerAddress(c.baseURL)
	if err != nil {
		return "", err
	}
	createPath := fmt.Sprintf("api/v1/projects/%s/folders/%s/dashboards", projectName, folderName)
	u.Path = path.Join(u.Path, createPath)
	return u.String(), nil
}

func (c *Client) DeleteDashboardByName(ref *openvizapi.PersesDashboardReference) error {
	url, err := c.deleteDashboardByNameAPIEndpoint(ref.ProjectName, ref.FolderName, ref.Name)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodDelete, url, nil)
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

	// Perses returns 2xx on success (204 No Content for delete) and 404 if it is already gone.
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("failed to delete perses dashboard %q: perses server returned %d %s: %s",
			ref.Name, resp.StatusCode, http.StatusText(resp.StatusCode), readResponseBody(resp))
	}
	return nil
}

// readResponseBody returns the (truncated, trimmed) response body so the actual
// reason reported by the Perses server is surfaced to the caller instead of a
// generic "unknown reason" message.
func readResponseBody(resp *http.Response) string {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return fmt.Sprintf("<failed to read response body: %v>", err)
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return "<empty response body>"
	}
	return msg
}

func encodeCertPEM(cert *x509.Certificate) []byte {
	block := pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	}
	return pem.EncodeToMemory(&block)
}

func (c *Client) SetDashboard(ctx context.Context, dashboard v1.Dashboard, projectName, folderName string) error {
	data, err := json.Marshal(dashboard)
	if err != nil {
		return err
	}

	url, err := c.createDashboardAPIEndpoint(projectName, folderName)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
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

	// Perses returns 2xx on success and 409 Conflict when the dashboard already exists.
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("failed to create perses dashboard %q: perses server returned %d %s: %s",
			dashboard.Metadata.Name, resp.StatusCode, http.StatusText(resp.StatusCode), readResponseBody(resp))
	}

	return nil
}
