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

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kmapi "kmodules.xyz/client-go/api/v1"
)

const (
	ResourceKindGrafanaDashboard = "GrafanaDashboard"
	ResourceGrafanaDashboard     = "grafanadashboard"
	ResourceGrafanaDashboards    = "grafanadashboards"
)

const (
	GrafanaNamespaceKey      = ".grafana.namespace"
	GrafanaNameKey           = ".grafana.name"
	GrafanaDashboardTitleKey = ".dashboard.title"
	DefaultGrafanaKey        = "openviz.dev/is-default-grafana"
)

// +genclient
// +k8s:openapi-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=grafanadashboards,singular=grafanadashboard,categories={grafana,openviz,appscode}
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
type GrafanaDashboard struct {
	metav1.TypeMeta   `json:",inline,omitempty"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Spec              GrafanaDashboardSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status            GrafanaDashboardStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

type GrafanaDashboardSpec struct {
	// GrafanaRef defines the grafana app binding name for the GrafanaDashboard
	// +optional
	GrafanaRef *kmapi.ObjectReference `json:"grafanaRef,omitempty" protobuf:"bytes,1,opt,name=grafanaRef"`

	// FolderID defines the Grafana folderID
	// +optional
	FolderID *int64 `json:"folderID,omitempty" protobuf:"varint,2,opt,name=folderID"`

	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Model *runtime.RawExtension `json:"model,omitempty" protobuf:"bytes,3,opt,name=model"`

	// Overwrite defines the existing grafanadashboard with the same name(if any) should be overwritten or not
	// +optional
	Overwrite bool `json:"overwrite,omitempty" protobuf:"varint,4,opt,name=overwrite"`

	// Templatize defines the fields which supports templating in GrafanaRef GrafanaDashboard Model json
	// +optional
	Templatize *ModelTemplateConfiguration `json:"templatize,omitempty" protobuf:"bytes,5,opt,name=templatize"`
}

type ModelTemplateConfiguration struct {
	Title      bool `json:"title,omitempty" protobuf:"bytes,1,opt,name=title"`
	Datasource bool `json:"datasource,omitempty" protobuf:"bytes,2,opt,name=datasource"`
}

type GrafanaDashboardReference struct {
	ID      *int64  `json:"id,omitempty" protobuf:"varint,1,opt,name=id"`
	UID     *string `json:"uid,omitempty" protobuf:"bytes,2,opt,name=uid"`
	OrgID   *int64  `json:"orgID,omitempty" protobuf:"varint,3,opt,name=orgID"`
	Slug    *string `json:"slug,omitempty" protobuf:"bytes,4,opt,name=title"`
	URL     *string `json:"url,omitempty" protobuf:"bytes,5,opt,name=url"`
	Version *int64  `json:"version,omitempty" protobuf:"varint,6,opt,name=version"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

type GrafanaDashboardList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Items           []GrafanaDashboard `json:"items,omitempty" protobuf:"bytes,2,rep,name=items"`
}

type GrafanaDashboardStatus struct {
	// ObservedGeneration is the most recent generation observed for this resource. It corresponds to the
	// resource's generation, which is updated on mutation by the API Server.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty" protobuf:"varint,1,opt,name=observedGeneration"`

	// Phase indicates the state this Vault cluster jumps in.
	// +optional
	Phase GrafanaPhase `json:"phase,omitempty" protobuf:"bytes,2,opt,name=phase,casttype=ClusterPhase"`

	// The reason for the current phase
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,3,opt,name=reason"`

	// Dashboard indicates the updated grafanadashboard database
	// +optional
	Dashboard *GrafanaDashboardReference `json:"dashboard,omitempty" protobuf:"bytes,4,opt,name=dashboard"`

	// Represents the latest available observations of a GrafanaDashboard current state.
	// +optional
	Conditions []kmapi.Condition `json:"conditions,omitempty" protobuf:"bytes,5,rep,name=conditions"`
}
