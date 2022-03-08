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

package e2e_test

import (
	"os"
	"path/filepath"

	"go.openviz.dev/grafana-tools/pkg/operator/cmds/server"

	"k8s.io/client-go/util/homedir"
)

type E2EOptions struct {
	*server.ExtraOptions

	kubeContext string
	kubeConfig  string

	username string
	password string
}

var options = &E2EOptions{
	ExtraOptions: server.NewExtraOptions(),

	kubeConfig: func() string {
		kubecfg := os.Getenv("KUBECONFIG")
		if kubecfg != "" {
			return kubecfg
		}
		return filepath.Join(homedir.HomeDir(), ".kube", "config")
	}(),
	username: "admin",
	password: "admin",
}

/*
func init() {
	options.AddGoFlags(flag.CommandLine)
	flag.StringVar(&options.kubeConfig, "kubeconfig", options.kubeConfig, "Path to kubeconfig file with authorization information (the master location is set by the master flag).")
	flag.StringVar(&options.kubeContext, "kube-context", "", "Name of kube context")
	flag.StringVar(&options.apiURL, "apiurl", options.apiURL, "API URL for grafana server")
	flag.StringVar(&options.apiKey, "apikey", options.apiKey, "User credential to connect ot grafana server")
	flag.Parse()
}*/
