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
	kmapi "kmodules.xyz/client-go/api/v1"
)

const (
	ResourceKindEmbeddedDashboard = "EmbeddedDashboard"
	ResourceEmbeddedDashboard     = "embeddeddashboard"
	ResourceEmbeddedDashboards    = "embeddeddashboards"
)

type EmbeddedDashboardRequest struct {
	Target    kmapi.ObjectID `json:"target" protobuf:"bytes,1,opt,name=target"`
	Dashboard DashboardRef   `json:"dashboard" protobuf:"bytes,2,opt,name=dashboard"`
	// +optional
	Panels []string `json:"panels,omitempty" protobuf:"bytes,3,rep,name=panels"`
}

type DashboardRef struct {
	// +optional
	*kmapi.ObjectReference `json:",inline" protobuf:"bytes,1,opt,name=objectReference"`
	// +optional
	Title string `json:"title,omitempty" protobuf:"bytes,2,opt,name=title"`
}

type PanelURL struct {
	Title       string `json:"title,omitempty" protobuf:"bytes,1,opt,name=title"`
	EmbeddedURL string `json:"embeddedURL,omitempty" protobuf:"bytes,2,opt,name=embeddedURL"`
}

type EmbeddedDashboardResponse struct {
	URLs []PanelURL `json:"urls,omitempty" protobuf:"bytes,1,rep,name=urls"`
}

// EmbeddedDashboard is the Schema for the EmbeddedDashboards API

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type EmbeddedDashboard struct {
	metav1.TypeMeta `json:",inline"`

	Request  *EmbeddedDashboardRequest  `json:"request,omitempty" protobuf:"bytes,1,opt,name=request"`
	Response *EmbeddedDashboardResponse `json:"response,omitempty" protobuf:"bytes,2,opt,name=response"`
}
