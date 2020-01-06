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
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	ResourceKindDashboard = "Dashboard"
	ResourceDashboard     = "dashboard"
	ResourceDashboards    = "dashboards"
)

// +genclient
// +k8s:openapi-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=dashboards,singular=dashboard,categories={grafana,searchlight,appscode}
// +kubebuilder:subresource:status
type Dashboard struct {
	metav1.TypeMeta   `json:",inline,omitempty"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Spec              DashboardSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status            DashboardStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

type DashboardSpec struct {
	Grafana TargetRef `json:"grafana" protobuf:"bytes,1,opt,name=grafana"`
	// +optional
	// +kubebuilder:validation:EmbeddedResource
	// +kubebuilder:pruning:PreserveUnknownFields
	Model             *runtime.RawExtension `json:"model,omitempty" protobuf:"bytes,2,opt,name=model"`
	HostedDashboardID int64                 `json:"hostedDashboardID,omitempty" protobuf:"varint,3,opt,name=hostedDashboardID"`

	Dashboard DashboardReference `json:"dashboard" protobuf:"bytes,4,opt,name=dashboard"`
	FolderID  int64              `json:"folderId" protobuf:"varint,5,opt,name=folderId"`
	Overwrite bool               `json:"overwrite" protobuf:"varint,6,opt,name=overwrite"`
}

type TargetRef struct {
	APIVersion string `json:"apiVersion,omitempty" protobuf:"bytes,1,opt,name=apiVersion"`
	Kind       string `json:"kind,omitempty" protobuf:"bytes,2,opt,name=kind"`
	Name       string `json:"name,omitempty" protobuf:"bytes,3,opt,name=name"`
}

type DashboardReference struct {
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

type DashboardList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Items           []Dashboard `json:"items,omitempty" protobuf:"bytes,2,rep,name=items"`
}

type ClusterPhase string

const (
	ClusterPhaseProcessing    ClusterPhase = "Processing"
	ClusterPhaseUnInitialized ClusterPhase = "Uninitialized"
	ClusterPhaseRunning       ClusterPhase = "Running"
	ClusterPhaseSealed        ClusterPhase = "Sealed"
)

type DashboardStatus struct {
	// ObservedGeneration is the most recent generation observed for this resource. It corresponds to the
	// resource's generation, which is updated on mutation by the API Server.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty" protobuf:"varint,1,opt,name=observedGeneration"`

	// Phase indicates the state this Vault cluster jumps in.
	// +optional
	Phase ClusterPhase `json:"phase,omitempty" protobuf:"bytes,2,opt,name=phase,casttype=ClusterPhase"`

	// Represents the latest available observations of a Dashboard current state.
	// +optional
	Conditions []DashboardCondition `json:"conditions,omitempty" protobuf:"bytes,8,rep,name=conditions"`
}

type DashboardConditionType string

// These are valid conditions of a Dashboard.
const (
	DashboardConditionFailure DashboardConditionType = "Failure"
)

// DashboardCondition describes the state of a Dashboard at a certain point.
type DashboardCondition struct {
	// Type of DashboardCondition condition.
	// +optional
	Type DashboardConditionType `json:"type,omitempty" protobuf:"bytes,1,opt,name=type,casttype=DashboardConditionType"`

	// Status of the condition, one of True, False, Unknown.
	// +optional
	Status core.ConditionStatus `json:"status,omitempty" protobuf:"bytes,2,opt,name=status,casttype=k8s.io/api/core/v1.ConditionStatus"`

	// The reason for the condition's.
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,3,opt,name=reason"`

	// A human readable message indicating details about the transition.
	// +optional
	Message string `json:"message,omitempty" protobuf:"bytes,4,opt,name=message"`
}
