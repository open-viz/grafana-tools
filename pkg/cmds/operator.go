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

package cmds

import (
	"context"

	"go.openviz.dev/grafana-tools/pkg/cmds/server"

	"github.com/spf13/cobra"
	v "gomodules.xyz/x/version"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
)

func NewCmdOperator(ctx context.Context) *cobra.Command {
	// o := server.NewOperatorOptions()
	o := server.NewOperatorOptions()

	cmd := &cobra.Command{
		Use:               "operator",
		Short:             "Launch Grafana Provisioner",
		Long:              "Launch Grafana Provisioner",
		DisableAutoGenTag: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			klog.Infof("Starting operator version %s+%s ...", v.Version.Version, v.Version.CommitHash)

			if err := o.Complete(); err != nil {
				return err
			}
			if err := o.Validate(); err != nil {
				return errors.NewAggregate(err)
			}
			return o.Run(ctx)
		},
	}

	o.AddFlags(cmd.Flags())

	return cmd
}
