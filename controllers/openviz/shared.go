package openviz

import (
	"context"
	"errors"
	"fmt"

	openvizv1alpha1 "go.openviz.dev/grafana-tools/apis/openviz/v1alpha1"
	appcatalog "kmodules.xyz/custom-resources/apis/appcatalog/v1alpha1"

	sdk "go.openviz.dev/grafana-sdk"
	core "k8s.io/api/core/v1"
	kmapi "kmodules.xyz/client-go/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func getAppBinding(ctx context.Context, cc client.Client, ref *kmapi.ObjectReference) (*appcatalog.AppBinding, error) {
	ab := &appcatalog.AppBinding{}
	if ref != nil {
		if err := cc.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, ab); err != nil {
			return nil, err
		}
	} else {
		abList := &appcatalog.AppBindingList{}
		opts := &client.ListOptions{Namespace: ""}
		selector := client.MatchingLabels{
			openvizv1alpha1.DefaultGrafanaKey: "true",
		}
		selector.ApplyToList(opts)
		if err := cc.List(ctx, abList, opts); err != nil {
			return nil, err
		}
		if len(abList.Items) != 1 {
			return nil, fmt.Errorf("expected one AppBinding with labelKey %q but got %v", openvizv1alpha1.DefaultGrafanaKey, len(abList.Items))
		}
		ab = &abList.Items[0]
	}
	return ab, nil
}

func getGrafanaClient(ctx context.Context, cc client.Client, ref *kmapi.ObjectReference) (*sdk.Client, error) {
	ab, err := getAppBinding(ctx, cc, ref)
	if err != nil {
		return nil, err
	}
	auth := &core.Secret{}
	if err := cc.Get(ctx, client.ObjectKey{Namespace: ab.Namespace, Name: ab.Spec.Secret.Name}, auth); err != nil {
		return nil, err
	}
	gURL, err := ab.URL()
	if err != nil {
		return nil, err
	}
	apiKey, ok := auth.Data["apiKey"]
	if !ok {
		return nil, errors.New("apiKey is not provided")
	}
	gc, err := sdk.NewClient(gURL, string(apiKey))
	if err != nil {
		return nil, err
	}
	return gc, nil
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
