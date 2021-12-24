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

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	ResourceKindGrafanaConfiguration = "GrafanaConfiguration"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GrafanaConfiguration defines configuration for a GrafanaRef AppBinding
type GrafanaConfiguration struct {
	metav1.TypeMeta `json:",inline,omitempty"`

	// Datasource defines the grafana grafanadatasource name.
	// +optional
	Datasource string `json:"datasource,omitempty" protobuf:"bytes,1,opt,name=datasource"`

	// FolderID defines the GrafanaRef folderID
	// +optional
	FolderID *int64 `json:"folderID,omitempty" protobuf:"varint,2,opt,name=folderID"`
}
