/*
Copyright AppsCode Inc. and Contributors.

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

package server

import (
	"os"

	"go.openviz.dev/grafana-tools/pkg/apiserver"

	"github.com/pkg/errors"
	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	"github.com/spf13/pflag"
	"sigs.k8s.io/yaml"
)

type ExtraOptions struct {
	QPS   float64
	Burst int

	BaseURL string
	Token   string
	CAFile  string

	HubUID string

	RancherAuthSecret      string
	AlertmanagerConfigFile string
}

func NewExtraOptions() *ExtraOptions {
	return &ExtraOptions{
		QPS:   1e6,
		Burst: 1e6,
	}
}

func (s *ExtraOptions) AddFlags(fs *pflag.FlagSet) {
	fs.Float64Var(&s.QPS, "qps", s.QPS, "The maximum QPS to the master from this client")
	fs.IntVar(&s.Burst, "burst", s.Burst, "The maximum burst for throttle")
	fs.StringVar(&s.BaseURL, "baseURL", s.BaseURL, "License server base url")
	fs.StringVar(&s.Token, "token", s.Token, "License server token")
	fs.StringVar(&s.CAFile, "platform-ca-file", s.Token, "Path to platform CA cert file")
	fs.StringVar(&s.HubUID, "hubUID", s.Token, "Cluster UID of ocm Hub")
	fs.StringVar(&s.RancherAuthSecret, "rancher-auth-secret", s.RancherAuthSecret, "Name of Rancher auth secret")
	fs.StringVar(&s.AlertmanagerConfigFile, "alertmanager-config-file", s.AlertmanagerConfigFile, "Path to alertmanager controller config file")
}

func (s *ExtraOptions) ApplyTo(cfg *apiserver.ExtraConfig) error {
	cfg.BaseURL = s.BaseURL
	cfg.Token = s.Token
	cfg.HubUID = s.HubUID
	cfg.RancherAuthSecret = s.RancherAuthSecret
	if s.AlertmanagerConfigFile != "" {
		data, err := os.ReadFile(s.AlertmanagerConfigFile)
		if err != nil {
			return errors.Wrapf(err, "failed to read alertmanager config file %s", s.AlertmanagerConfigFile)
		}
		var amcfg monitoringv1alpha1.AlertmanagerConfigSpec
		if err := yaml.UnmarshalStrict(data, &amcfg); err != nil {
			return errors.Wrapf(err, "failed to parse alertmanager config file %s", s.AlertmanagerConfigFile)
		}
		cfg.Alertmanager = amcfg
	}
	if s.CAFile != "" {
		caCert, err := os.ReadFile(s.CAFile)
		if err != nil {
			return errors.Wrapf(err, "failed to read CA file %s", s.CAFile)
		}
		cfg.CACert = caCert
	}

	cfg.ClientConfig.QPS = float32(s.QPS)
	cfg.ClientConfig.Burst = s.Burst

	return nil
}

func (s *ExtraOptions) Validate() []error {
	return nil
}
