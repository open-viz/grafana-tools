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
	cs "go.searchlight.dev/grafana-operator/client/clientset/versioned"

	"github.com/appscode/go/crypto/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	appcat_cs "kmodules.xyz/custom-resources/client/clientset/versioned/typed/appcatalog/v1alpha1"
)

type Framework struct {
	restConfig   *rest.Config
	kubeClient   kubernetes.Interface
	extClient    cs.Interface
	appcatClient appcat_cs.AppcatalogV1alpha1Interface

	namespace string
	name      string
}

func New(
	restConfig *rest.Config,
	kubeClient kubernetes.Interface,
	extClient cs.Interface,
	appcatClient appcat_cs.AppcatalogV1alpha1Interface,
) *Framework {
	return &Framework{
		restConfig:   restConfig,
		kubeClient:   kubeClient,
		extClient:    extClient,
		appcatClient: appcatClient,

		name:      rand.WithUniqSuffix("grafana-operator"),
		namespace: rand.WithUniqSuffix("grafana"),
	}
}

func (f *Framework) Invoke() *Invocation {
	return &Invocation{
		Framework: f,
		app:       rand.WithUniqSuffix("grafana-operator-e2e"),
	}
}

func (f *Framework) Name() string {
	return f.name
}

func (fi *Invocation) KubeClient() kubernetes.Interface {
	return fi.kubeClient
}

func (fi *Invocation) RestConfig() *rest.Config {
	return fi.restConfig
}

type Invocation struct {
	*Framework
	app string
}
