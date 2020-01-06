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

package admission

import (
	"fmt"
	"strings"
	"sync"

	api "go.searchlight.dev/grafana-operator/apis/grafana/v1alpha1"
	cs "go.searchlight.dev/grafana-operator/client/clientset/versioned"

	admission "k8s.io/api/admission/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/mergepatch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	meta_util "kmodules.xyz/client-go/meta"
	hookapi "kmodules.xyz/webhook-runtime/admission/v1beta1"
)

const (
	validatorGroup   = "validators.grafana.searchlight.dev"
	validatorVersion = "v1alpha1"
)

type DashboardValidator struct {
	client      kubernetes.Interface
	extClient   cs.Interface
	lock        sync.RWMutex
	initialized bool
}

var _ hookapi.AdmissionHook = &DashboardValidator{}

func (v *DashboardValidator) Resource() (plural schema.GroupVersionResource, singular string) {
	return schema.GroupVersionResource{
			Group:    validatorGroup,
			Version:  validatorVersion,
			Resource: "dashboardvalidators",
		},
		"dashboardvalidator"
}

func (v *DashboardValidator) Initialize(config *rest.Config, stopCh <-chan struct{}) error {
	v.lock.Lock()
	defer v.lock.Unlock()

	v.initialized = true

	var err error
	if v.client, err = kubernetes.NewForConfig(config); err != nil {
		return err
	}
	if v.extClient, err = cs.NewForConfig(config); err != nil {
		return err
	}
	return err
}

func (v *DashboardValidator) Admit(req *admission.AdmissionRequest) *admission.AdmissionResponse {
	status := &admission.AdmissionResponse{}

	if (req.Operation != admission.Create && req.Operation != admission.Update && req.Operation != admission.Delete) ||
		len(req.SubResource) != 0 ||
		req.Kind.Group != api.SchemeGroupVersion.Group ||
		req.Kind.Kind != api.ResourceKindDashboard {
		status.Allowed = true
		return status
	}

	v.lock.RLock()
	defer v.lock.RUnlock()
	if !v.initialized {
		return hookapi.StatusUninitialized()
	}

	if req.Operation == admission.Create || req.Operation == admission.Update {
		obj, err := meta_util.UnmarshalFromJSON(req.Object.Raw, api.SchemeGroupVersion)
		if err != nil {
			return hookapi.StatusBadRequest(err)
		}
		if req.Operation == admission.Update {
			// validate changes made by user
			oldObject, err := meta_util.UnmarshalFromJSON(req.OldObject.Raw, api.SchemeGroupVersion)
			if err != nil {
				return hookapi.StatusBadRequest(err)
			}

			vs := obj.(*api.Dashboard).DeepCopy()
			oldVs := oldObject.(*api.Dashboard).DeepCopy()

			if err := validateUpdate(vs, oldVs); err != nil {
				return hookapi.StatusBadRequest(err)
			}
		}
		// validate dashboard specs
		if err = ValidateDashboard(v.client, v.extClient, obj.(*api.Dashboard)); err != nil {
			return hookapi.StatusForbidden(err)
		}
	}
	status.Allowed = true
	return status
}

// ValidateDashboard checks if the object satisfies all the requirements.
// It is not method of Interface, because it is referenced from controller package too.
func ValidateDashboard(client kubernetes.Interface, extClient cs.Interface, vs *api.Dashboard) error {
	return nil
}

func validateUpdate(obj, oldObj runtime.Object) error {
	preconditions := getPreconditionFunc()
	_, err := meta_util.CreateStrategicPatch(oldObj, obj, preconditions...)
	if err != nil {
		if mergepatch.IsPreconditionFailed(err) {
			return fmt.Errorf("%v.%v", err, preconditionFailedError())
		}
		return err
	}
	return nil
}

func getPreconditionFunc() []mergepatch.PreconditionFunc {
	preconditions := []mergepatch.PreconditionFunc{
		mergepatch.RequireKeyUnchanged("apiVersion"),
		mergepatch.RequireKeyUnchanged("kind"),
		mergepatch.RequireMetadataKeyUnchanged("name"),
		mergepatch.RequireMetadataKeyUnchanged("namespace"),
	}

	for _, field := range preconditionSpecFields {
		preconditions = append(preconditions,
			meta_util.RequireChainKeyUnchanged(field),
		)
	}
	return preconditions
}

var preconditionSpecFields = []string{
	"spec.unsealer",
	"spec.backend",
	"spec.podTemplate.spec.nodeSelector",
}

func preconditionFailedError() error {
	str := preconditionSpecFields
	strList := strings.Join(str, "\n\t")
	return fmt.Errorf(strings.Join([]string{`At least one of the following was changed:
	apiVersion
	kind
	name
	namespace`, strList}, "\n\t"))
}
