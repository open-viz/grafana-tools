/*
Copyright The Searchlight Authors.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ResourceKindDashboardTemplate = "DashboardTemplate"
	ResourceDashboardTemplate     = "dashboardtemplate"
	ResourceDashboardTemplates    = "dashboardtemplates"
)

// +genclient
// +k8s:openapi-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=dashboardtemplates,singular=dashboardtemplate,scope=Cluster,categories={grafana,searchlight,appscode}
// +kubebuilder:subresource:status
type DashboardTemplate struct {
	metav1.TypeMeta   `json:",inline,omitempty"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Spec              DashboardTemplateSpec `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
}

type DashboardTemplateSpec struct {
	DashboardTemplate DashboardTemplateReference `json:"dashboardtemplate" protobuf:"bytes,1,opt,name=dashboardtemplate"`
	FolderID          int64                      `json:"folderId" protobuf:"varint,2,opt,name=folderId"`
	Overwrite         bool                       `json:"overwrite" protobuf:"varint,3,opt,name=overwrite"`
}

type DashboardTemplateReference struct {
	ID            *int64   `json:"id" protobuf:"varint,1,opt,name=id"`
	UID           *string  `json:"uid" protobuf:"bytes,2,opt,name=uid"`
	Title         string   `json:"title" protobuf:"bytes,3,opt,name=title"`
	Tags          []string `json:"tags" protobuf:"bytes,4,rep,name=tags"`
	Timezone      string   `json:"timezone" protobuf:"bytes,5,opt,name=timezone"`
	SchemaVersion int64    `json:"schemaVersion" protobuf:"varint,6,opt,name=schemaVersion"`
	Version       int64    `json:"version" protobuf:"varint,7,opt,name=version"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

type DashboardTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Items           []DashboardTemplate `json:"items,omitempty" protobuf:"bytes,2,rep,name=items"`
}
