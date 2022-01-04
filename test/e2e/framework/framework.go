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

package framework

import (
	"gomodules.xyz/x/crypto/rand"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Framework struct {
	restConfig *rest.Config
	cc         client.Client

	namespace string
	name      string
}

func New(
	restConfig *rest.Config,
	cc client.Client,
) *Framework {
	return &Framework{
		restConfig: restConfig,
		cc:         cc,

		name:      rand.WithUniqSuffix("grafana-tools"),
		namespace: rand.WithUniqSuffix("grafana"),
	}
}

func (f *Framework) Invoke() *Invocation {
	return &Invocation{
		Framework: f,
		app:       rand.WithUniqSuffix("grafana-tools-e2e"),
	}
}

func (f *Framework) Name() string {
	return f.name
}

func (fi *Invocation) RestConfig() *rest.Config {
	return fi.restConfig
}

type Invocation struct {
	*Framework
	app string
}
