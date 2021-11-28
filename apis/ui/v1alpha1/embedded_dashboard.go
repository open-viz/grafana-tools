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

var (
	GrafanaNameKey    = ".grafana.name"
	DashboardTitleKey = ".dashboard.title"
)

type EmbeddedDashboardRequest struct {
	Ref         DashboardRef `json:"ref" protobuf:"bytes,1,opt,name=ref"`
	PanelTitles []string     `json:"panelTitles,omitempty" protobuf:"bytes,2,rep,name=panelTitles"`
}

type DashboardRef struct {
	Name     *string            `json:"name,omitempty" protobuf:"bytes,1,opt,name=name"`
	Selector *DashboardSelector `json:"selector,omitempty" protobuf:"bytes,2,opt,name=selector"`
}

type DashboardSelector struct {
	GrafanaName    string `json:"grafanaName,omitempty" protobuf:"bytes,1,opt,name=grafanaName"`
	DashboardTitle string `json:"dashboardTitle,omitempty" protobuf:"bytes,2,opt,name=dashboardTitle"`
}

type PanelURL struct {
	Title       string `json:"title,omitempty" protobuf:"bytes,1,opt,name=title"`
	EmbeddedURL string `json:"embeddedURL,omitempty" protobuf:"bytes,2,opt,name=embeddedURL"`
}

type EmbeddedDashboardResponse struct {
	URLs []PanelURL `json:"urls,omitempty" protobuf:"bytes,1,rep,name=urls"`
}

// EmbeddedDashboardStatus defines the observed state of EmbeddedDashboard
type EmbeddedDashboardStatus struct {
}

// EmbeddedDashboard is the Schema for the EmbeddedDashboards API

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type EmbeddedDashboard struct {
	metav1.TypeMeta `json:",inline"`

	Request  *EmbeddedDashboardRequest  `json:"request,omitempty" protobuf:"bytes,1,opt,name=request"`
	Response *EmbeddedDashboardResponse `json:"response,omitempty" protobuf:"bytes,2,opt,name=response"`
}
