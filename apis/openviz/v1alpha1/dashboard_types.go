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
	ResourceKindDashboard = "Dashboard"
	ResourceDashboard     = "dashboard"
	ResourceDashboards    = "dashboards"
)

const (
	GrafanaNameKey    = ".grafana.name"
	DashboardTitleKey = ".dashboard.title"
)

// +genclient
// +k8s:openapi-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=dashboards,singular=dashboard,categories={grafana,openviz,appscode}
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
type Dashboard struct {
	metav1.TypeMeta   `json:",inline,omitempty"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Spec              DashboardSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status            DashboardStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

type DashboardSpec struct {
	// Grafana defines the grafana app binding name for the Dashboard
	Grafana *TargetRef `json:"grafana,omitempty" protobuf:"bytes,1,opt,name=grafana"`
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Model *runtime.RawExtension `json:"model,omitempty" protobuf:"bytes,2,opt,name=model"`

	// FolderID defines the Grafana folderID
	// +optional
	FolderID int64 `json:"folderID,omitempty" protobuf:"varint,3,opt,name=folderID"`

	// Overwrite defines the existing dashboard with the same name(if any) should be overwritten or not
	// +optional
	Overwrite bool `json:"overwrite,omitempty" protobuf:"varint,4,opt,name=overwrite"`

	// Templatize defines the fields which supports templating in Grafana Dashboard Model json
	// +optional
	Templatize *ModelTemplateConfiguration `json:"templatize,omitempty" protobuf:"bytes,5,opt,name=templatize"`
}

type ModelTemplateConfiguration struct {
	Title      bool `json:"title,omitempty" protobuf:"bytes,1,opt,name=title"`
	Datasource bool `json:"datasource,omitempty" protobuf:"bytes,2,opt,name=datasource"`
}

type TargetRef struct {
	APIGroup string `json:"apiGroup,omitempty" protobuf:"bytes,1,opt,name=apiGroup"`
	Kind     string `json:"kind,omitempty" protobuf:"bytes,2,opt,name=kind"`
	Name     string `json:"name,omitempty" protobuf:"bytes,3,opt,name=name"`
}

type DashboardReference struct {
	ID      *int64  `json:"id,omitempty" protobuf:"varint,1,opt,name=id"`
	UID     *string `json:"uid,omitempty" protobuf:"bytes,2,opt,name=uid"`
	OrgID   *int64  `json:"orgID,omitempty" protobuf:"varint,3,opt,name=orgID"`
	Slug    *string `json:"slug,omitempty" protobuf:"bytes,4,opt,name=title"`
	URL     *string `json:"url,omitempty" protobuf:"bytes,5,opt,name=url"`
	Version *int64  `json:"version,omitempty" protobuf:"varint,6,opt,name=version"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

type DashboardList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Items           []Dashboard `json:"items,omitempty" protobuf:"bytes,2,rep,name=items"`
}

type DashboardStatus struct {
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

	// Dashboard indicates the updated dashboard database
	// +optional
	Dashboard *DashboardReference `json:"dashboard,omitempty" protobuf:"bytes,4,opt,name=dashboard"`

	// Represents the latest available observations of a Dashboard current state.
	// +optional
	Conditions []kmapi.Condition `json:"conditions,omitempty" protobuf:"bytes,5,rep,name=conditions"`
}
