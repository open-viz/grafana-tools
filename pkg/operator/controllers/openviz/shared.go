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

package openviz

import (
	"context"

	sdk "go.openviz.dev/grafana-sdk"
	openvizapi "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"

	core "k8s.io/api/core/v1"
	kmapi "kmodules.xyz/client-go/api/v1"
	"kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewGrafanaClient(ctx context.Context, kc client.Client, ref *kmapi.ObjectReference) (*sdk.Client, error) {
	ab, err := openvizapi.GetGrafana(ctx, kc, ref)
	if err != nil {
		return nil, err
	}

	var authSecret *core.Secret
	if ab.Spec.Secret != nil && ab.Spec.Secret.Name != "" {
		var sec core.Secret
		if err := kc.Get(ctx, client.ObjectKey{Namespace: ab.Namespace, Name: ab.Spec.Secret.Name}, &sec); err != nil {
			return nil, err
		}
		authSecret = &sec
	}

	addr, auth, err := ExtractGrafanaConfig(ab, authSecret)
	if err != nil {
		return nil, err
	}

	gc, err := sdk.NewClient(addr, auth)
	if err != nil {
		return nil, err
	}
	return gc, nil
}

func ExtractGrafanaConfig(ab *v1alpha1.AppBinding, authSecret *core.Secret) (string, *sdk.AuthConfig, error) {
	addr, err := ab.URL()
	if err != nil {
		return "", nil, err
	}

	var auth *sdk.AuthConfig
	if authSecret != nil {
		if token, ok := authSecret.Data["token"]; ok {
			auth = &sdk.AuthConfig{
				BearerToken: string(token),
			}
		} else {
			auth = &sdk.AuthConfig{
				BasicAuth: &sdk.BasicAuth{},
			}
			if v, ok := authSecret.Data[core.BasicAuthUsernameKey]; ok {
				auth.BasicAuth.Username = string(v)
			}
			if v, ok := authSecret.Data[core.BasicAuthPasswordKey]; ok {
				auth.BasicAuth.Password = string(v)
			}
		}
	}
	return addr, auth, nil
}

// Helper functions to check a string is present from a slice of strings.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
