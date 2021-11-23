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
)

const (
	ResourceKindEmbeddedDashboard = "EmbeddedDashboard"
	ResourceEmbeddedDashboard     = "embeddeddashboard"
	ResourceEmbeddedDashboards    = "embeddeddashboards"
)

// EmbeddedDashboardSpec defines the desired state of EmbeddedDashboard
type EmbeddedDashboardSpec struct {
}

// EmbeddedDashboardStatus defines the observed state of EmbeddedDashboard
type EmbeddedDashboardStatus struct {
}

// EmbeddedDashboard is the Schema for the EmbeddedDashboards API

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type EmbeddedDashboard struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   EmbeddedDashboardSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status EmbeddedDashboardStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// EmbeddedDashboardList contains a list of EmbeddedDashboard

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type EmbeddedDashboardList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Items           []EmbeddedDashboard `json:"items" protobuf:"bytes,2,rep,name=items"`
}
