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

package controllers

import (
	api "go.openviz.dev/apimachinery/apis/openviz/v1alpha1"

	crd_cs "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/klog/v2"
	"kmodules.xyz/client-go/apiextensions"
	appcatalogapi "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
)

func EnsureCustomResourceDefinitions(client crd_cs.Interface) error {
	klog.Infoln("Ensuring CustomResourceDefinition...")
	crds := []*apiextensions.CustomResourceDefinition{
		api.GrafanaDashboard{}.CustomResourceDefinition(),
		api.GrafanaDatasource{}.CustomResourceDefinition(),
		appcatalogapi.AppBinding{}.CustomResourceDefinition(),
	}
	return apiextensions.RegisterCRDs(client, crds)
}
