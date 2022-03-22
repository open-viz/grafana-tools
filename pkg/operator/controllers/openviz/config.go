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

package openviz

import (
	"flag"
	"time"

	"github.com/spf13/pflag"
)

const (
	defaultRequeueAfterDuration = time.Minute
)

type RecommendationReconcileConfig struct {
	RequeueAfterDuration time.Duration
}

func NewRecommendationReconcileConfig() *RecommendationReconcileConfig {
	return &RecommendationReconcileConfig{}
}

func (c *RecommendationReconcileConfig) AddGoFlags(fs *flag.FlagSet) {
	fs.DurationVar(&c.RequeueAfterDuration, "requeue-after-duration", defaultRequeueAfterDuration, "Duration after the GrafanaDashboard object will be requeue when it is failed to create external grafana dashboard resource. The flag accepts a value acceptable to time.ParseDuration. Ref: https://pkg.go.dev/time#ParseDuration")
}

func (c *RecommendationReconcileConfig) AddFlags(fs *pflag.FlagSet) {
	pfs := flag.NewFlagSet("grafana-operator", flag.ExitOnError)
	c.AddGoFlags(pfs)
	fs.AddGoFlagSet(pfs)
}

func (c *RecommendationReconcileConfig) Validate() []error {
	errs := make([]error, 0)
	if _, err := time.ParseDuration(c.RequeueAfterDuration.String()); err != nil {
		errs = append(errs, err)
	}
	return errs
}
