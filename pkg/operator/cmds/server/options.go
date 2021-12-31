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

package server

import (
	"flag"
	"time"

	"github.com/spf13/pflag"
	"kmodules.xyz/client-go/tools/clusterid"
)

type ExtraOptions struct {
	MaxNumRequeues          int
	NumThreads              int
	QPS                     float64
	Burst                   int
	ResyncPeriod            time.Duration
	EnableValidatingWebhook bool
	EnableMutatingWebhook   bool
}

func NewExtraOptions() *ExtraOptions {
	return &ExtraOptions{
		MaxNumRequeues: 5,
		NumThreads:     2,
		QPS:            100,
		Burst:          100,
		ResyncPeriod:   10 * time.Minute,
	}
}

func (s *ExtraOptions) AddGoFlags(fs *flag.FlagSet) {
	clusterid.AddGoFlags(fs)

	fs.Float64Var(&s.QPS, "qps", s.QPS, "The maximum QPS to the master from this client")
	fs.IntVar(&s.Burst, "burst", s.Burst, "The maximum burst for throttle")
	fs.DurationVar(&s.ResyncPeriod, "resync-period", s.ResyncPeriod, "If non-zero, will re-list this often. Otherwise, re-list will be delayed aslong as possible (until the upstream source closes the watch or times out.")

	fs.BoolVar(&s.EnableMutatingWebhook, "enable-mutating-webhook", s.EnableMutatingWebhook, "If true, enables mutating webhooks for KubeVault CRDs.")
	fs.BoolVar(&s.EnableValidatingWebhook, "enable-validating-webhook", s.EnableValidatingWebhook, "If true, enables validating webhooks for KubeVault CRDs.")
}

func (s *ExtraOptions) AddFlags(fs *pflag.FlagSet) {
	pfs := flag.NewFlagSet("vault-server", flag.ExitOnError)
	s.AddGoFlags(pfs)
	fs.AddGoFlagSet(pfs)
}

//func (s *ExtraOptions) ApplyTo(cfg *controller.Config) error {
//	var err error
//
//	cfg.MaxNumRequeues = s.MaxNumRequeues
//	cfg.NumThreads = s.NumThreads
//	cfg.ResyncPeriod = s.ResyncPeriod
//	cfg.ClientConfig.QPS = float32(s.QPS)
//	cfg.ClientConfig.Burst = s.Burst
//	cfg.EnableMutatingWebhook = s.EnableMutatingWebhook
//	cfg.EnableValidatingWebhook = s.EnableValidatingWebhook
//
//	if cfg.KubeClient, err = kubernetes.NewForConfig(cfg.ClientConfig); err != nil {
//		return err
//	}
//	if cfg.ExtClient, err = cs.NewForConfig(cfg.ClientConfig); err != nil {
//		return err
//	}
//	if cfg.CRDClient, err = crd_cs.NewForConfig(cfg.ClientConfig); err != nil {
//		return err
//	}
//	if cfg.PromClient, err = prom.NewForConfig(cfg.ClientConfig); err != nil {
//		return err
//	}
//	if cfg.AppCatalogClient, err = appcat_cs.NewForConfig(cfg.ClientConfig); err != nil {
//		return err
//	}
//	return nil
//}
