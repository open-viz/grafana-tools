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
	ResourceKindDatasource = "Datasource"
	ResourceDatasource     = "datasource"
	ResourceDatasources    = "datasources"
)

// +genclient
// +k8s:openapi-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=datasources,singular=datasource,categories={grafana,searchlight,appscode}
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
type Datasource struct {
	metav1.TypeMeta   `json:",inline,omitempty"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Spec              DatasourceSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status            DatasourceStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

type DatasourceSpec struct {
	Grafana           *TargetRef           `json:"grafana" protobuf:"bytes,1,opt,name=grafana"`
	ID                int64                `json:"id,omitempty" protobuf:"bytes,2,opt,name=id"`
	OrgID             int64                `json:"orgId" protobuf:"bytes,3,opt,name=orgId"`
	Name              string               `json:"name" protobuf:"bytes,4,opt,name=name"`
	Type              DatasourceType       `json:"type" protobuf:"bytes,5,opt,name=type"`
	Access            DatasourceAccessType `json:"access" protobuf:"bytes,6,opt,name=access"`
	URL               string               `json:"url" protobuf:"bytes,7,opt,name=url"`
	Password          string               `json:"password,omitempty" protobuf:"bytes,8,opt,name=password"`
	User              string               `json:"user,omitempty" protobuf:"bytes,9,opt,name=user"`
	Database          string               `json:"database,omitempty" protobuf:"bytes,10,opt,name=database"`
	BasicAuth         bool                 `json:"basicAuth,omitempty" protobuf:"bytes,11,opt,name=basicAuth"`
	BasicAuthUser     string               `json:"basicAuthUser,omitempty" protobuf:"bytes,12,opt,name=basicAuthUser"`
	BasicAuthPassword string               `json:"basicAuthPassword,omitempty" protobuf:"bytes,13,opt,name=basicAuthPassword"`
	IsDefault         bool                 `json:"isDefault,omitempty" protobuf:"bytes,14,opt,name=isDefault"`
	Editable          bool                 `json:"editable,omitempty" protobuf:"bytes,15,opt,name=editable"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

type DatasourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Items           []Datasource `json:"items,omitempty" protobuf:"bytes,2,rep,name=items"`
}

type DatasourceStatus struct {
	// ObservedGeneration is the most recent generation observed for this resource. It corresponds to the
	// resource's generation, which is updated on mutation by the API Server.
	// +optional
	ObservedGeneration int64             `json:"observedGeneration,omitempty" protobuf:"varint,1,opt,name=observedGeneration"`
	DatasourceID       *int64            `json:"datasourceId,omitempty" protobuf:"bytes,2,opt,name=datasourceID"`
	Phase              GrafanaPhase      `json:"phase,omitempty" protobuf:"bytes,3,opt,name=phase"`
	Reason             string            `json:"reason,omitempty" protobuf:"bytes,4,opt,name=reason"`
	Conditions         []kmapi.Condition `json:"conditions,omitempty" protobuf:"bytes,5,rep,name=conditions"`
}
