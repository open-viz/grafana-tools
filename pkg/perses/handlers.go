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
	"net/http"
	"path"

	"go.openviz.dev/apimachinery/apis/openviz"
	openvizapi "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"

	v1 "github.com/perses/perses/pkg/model/api/v1"
	"go.bytebuilders.dev/license-verifier/info"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	deletePath := fmt.Sprintf("api/v1/projects/%s/folders/%s/dashboards", projectName, folderName)
	u.Path = path.Join(u.Path, deletePath)
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

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		return apierrors.NewGenericServerResponse(
			resp.StatusCode,
			http.MethodDelete,
			schema.GroupResource{Group: openviz.GroupName, Resource: openvizapi.ResourcePersesDashboards},
			"",
			"",
			0,
			false,
		)
	}
	return nil
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

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		return apierrors.NewGenericServerResponse(
			resp.StatusCode,
			http.MethodPost,
			schema.GroupResource{Group: openviz.GroupName, Resource: openvizapi.ResourcePersesDashboards},
			"",
			"",
			0,
			false,
		)
	}

	return nil
}
