//go:build !ignore_autogenerated
// +build !ignore_autogenerated

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

// Code generated by deepcopy-gen. DO NOT EDIT.

package v1alpha1

import (
	runtime "k8s.io/apimachinery/pkg/runtime"
	v1 "kmodules.xyz/client-go/api/v1"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaConfiguration) DeepCopyInto(out *GrafanaConfiguration) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	if in.FolderID != nil {
		in, out := &in.FolderID, &out.FolderID
		*out = new(int64)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaConfiguration.
func (in *GrafanaConfiguration) DeepCopy() *GrafanaConfiguration {
	if in == nil {
		return nil
	}
	out := new(GrafanaConfiguration)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *GrafanaConfiguration) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaDashboard) DeepCopyInto(out *GrafanaDashboard) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaDashboard.
func (in *GrafanaDashboard) DeepCopy() *GrafanaDashboard {
	if in == nil {
		return nil
	}
	out := new(GrafanaDashboard)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *GrafanaDashboard) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaDashboardList) DeepCopyInto(out *GrafanaDashboardList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]GrafanaDashboard, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaDashboardList.
func (in *GrafanaDashboardList) DeepCopy() *GrafanaDashboardList {
	if in == nil {
		return nil
	}
	out := new(GrafanaDashboardList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *GrafanaDashboardList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaDashboardReference) DeepCopyInto(out *GrafanaDashboardReference) {
	*out = *in
	if in.ID != nil {
		in, out := &in.ID, &out.ID
		*out = new(int64)
		**out = **in
	}
	if in.UID != nil {
		in, out := &in.UID, &out.UID
		*out = new(string)
		**out = **in
	}
	if in.OrgID != nil {
		in, out := &in.OrgID, &out.OrgID
		*out = new(int64)
		**out = **in
	}
	if in.Slug != nil {
		in, out := &in.Slug, &out.Slug
		*out = new(string)
		**out = **in
	}
	if in.URL != nil {
		in, out := &in.URL, &out.URL
		*out = new(string)
		**out = **in
	}
	if in.Version != nil {
		in, out := &in.Version, &out.Version
		*out = new(int64)
		**out = **in
	}
	if in.State != nil {
		in, out := &in.State, &out.State
		*out = new(string)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaDashboardReference.
func (in *GrafanaDashboardReference) DeepCopy() *GrafanaDashboardReference {
	if in == nil {
		return nil
	}
	out := new(GrafanaDashboardReference)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaDashboardSpec) DeepCopyInto(out *GrafanaDashboardSpec) {
	*out = *in
	if in.GrafanaRef != nil {
		in, out := &in.GrafanaRef, &out.GrafanaRef
		*out = new(v1.ObjectReference)
		**out = **in
	}
	if in.Model != nil {
		in, out := &in.Model, &out.Model
		*out = new(runtime.RawExtension)
		(*in).DeepCopyInto(*out)
	}
	if in.Templatize != nil {
		in, out := &in.Templatize, &out.Templatize
		*out = new(ModelTemplateConfiguration)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaDashboardSpec.
func (in *GrafanaDashboardSpec) DeepCopy() *GrafanaDashboardSpec {
	if in == nil {
		return nil
	}
	out := new(GrafanaDashboardSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaDashboardStatus) DeepCopyInto(out *GrafanaDashboardStatus) {
	*out = *in
	if in.Dashboard != nil {
		in, out := &in.Dashboard, &out.Dashboard
		*out = new(GrafanaDashboardReference)
		(*in).DeepCopyInto(*out)
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaDashboardStatus.
func (in *GrafanaDashboardStatus) DeepCopy() *GrafanaDashboardStatus {
	if in == nil {
		return nil
	}
	out := new(GrafanaDashboardStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaDashboardTemplate) DeepCopyInto(out *GrafanaDashboardTemplate) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaDashboardTemplate.
func (in *GrafanaDashboardTemplate) DeepCopy() *GrafanaDashboardTemplate {
	if in == nil {
		return nil
	}
	out := new(GrafanaDashboardTemplate)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *GrafanaDashboardTemplate) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaDashboardTemplateList) DeepCopyInto(out *GrafanaDashboardTemplateList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]GrafanaDashboardTemplate, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaDashboardTemplateList.
func (in *GrafanaDashboardTemplateList) DeepCopy() *GrafanaDashboardTemplateList {
	if in == nil {
		return nil
	}
	out := new(GrafanaDashboardTemplateList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *GrafanaDashboardTemplateList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaDashboardTemplateReference) DeepCopyInto(out *GrafanaDashboardTemplateReference) {
	*out = *in
	if in.ID != nil {
		in, out := &in.ID, &out.ID
		*out = new(int64)
		**out = **in
	}
	if in.UID != nil {
		in, out := &in.UID, &out.UID
		*out = new(string)
		**out = **in
	}
	if in.Tags != nil {
		in, out := &in.Tags, &out.Tags
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaDashboardTemplateReference.
func (in *GrafanaDashboardTemplateReference) DeepCopy() *GrafanaDashboardTemplateReference {
	if in == nil {
		return nil
	}
	out := new(GrafanaDashboardTemplateReference)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaDashboardTemplateSpec) DeepCopyInto(out *GrafanaDashboardTemplateSpec) {
	*out = *in
	in.GrafanaDashboardTemplate.DeepCopyInto(&out.GrafanaDashboardTemplate)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaDashboardTemplateSpec.
func (in *GrafanaDashboardTemplateSpec) DeepCopy() *GrafanaDashboardTemplateSpec {
	if in == nil {
		return nil
	}
	out := new(GrafanaDashboardTemplateSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaDatasource) DeepCopyInto(out *GrafanaDatasource) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaDatasource.
func (in *GrafanaDatasource) DeepCopy() *GrafanaDatasource {
	if in == nil {
		return nil
	}
	out := new(GrafanaDatasource)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *GrafanaDatasource) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaDatasourceList) DeepCopyInto(out *GrafanaDatasourceList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]GrafanaDatasource, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaDatasourceList.
func (in *GrafanaDatasourceList) DeepCopy() *GrafanaDatasourceList {
	if in == nil {
		return nil
	}
	out := new(GrafanaDatasourceList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *GrafanaDatasourceList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaDatasourceSpec) DeepCopyInto(out *GrafanaDatasourceSpec) {
	*out = *in
	if in.GrafanaRef != nil {
		in, out := &in.GrafanaRef, &out.GrafanaRef
		*out = new(v1.ObjectReference)
		**out = **in
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaDatasourceSpec.
func (in *GrafanaDatasourceSpec) DeepCopy() *GrafanaDatasourceSpec {
	if in == nil {
		return nil
	}
	out := new(GrafanaDatasourceSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GrafanaDatasourceStatus) DeepCopyInto(out *GrafanaDatasourceStatus) {
	*out = *in
	if in.GrafanaDatasourceID != nil {
		in, out := &in.GrafanaDatasourceID, &out.GrafanaDatasourceID
		*out = new(int64)
		**out = **in
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GrafanaDatasourceStatus.
func (in *GrafanaDatasourceStatus) DeepCopy() *GrafanaDatasourceStatus {
	if in == nil {
		return nil
	}
	out := new(GrafanaDatasourceStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ModelTemplateConfiguration) DeepCopyInto(out *ModelTemplateConfiguration) {
	*out = *in
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ModelTemplateConfiguration.
func (in *ModelTemplateConfiguration) DeepCopy() *ModelTemplateConfiguration {
	if in == nil {
		return nil
	}
	out := new(ModelTemplateConfiguration)
	in.DeepCopyInto(out)
	return out
}
