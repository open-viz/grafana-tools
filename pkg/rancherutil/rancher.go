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

package rancherutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rancher/norman/clientbase"
	rancher "github.com/rancher/rancher/pkg/client/generated/management/v3"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	RancherKeyBaseURL        = "baseURL"
	RancherKeyToken          = "token"
	RancherKeyTokenState     = "token.state"
	RancherKeyTokenNext      = "token.next"
	RancherKeyTokenExpiresAt = "token.expiresAt"
)

type RancherToken struct {
	UserID    string
	TokenID   string
	Token     string
	ExpiresAt time.Time
}

func FindToken(ctx context.Context, sec *core.Secret, state string) (*rancher.Client, *RancherToken, error) {
	log := log.FromContext(ctx)
	objKey := client.ObjectKeyFromObject(sec)

	requiredRancherKeys := []string{
		RancherKeyBaseURL,
		RancherKeyToken,
	}
	for _, key := range requiredRancherKeys {
		if v := sec.Data[key]; len(v) == 0 {
			return nil, nil, fmt.Errorf("rancher auth secret %s is missing key %s", objKey, key)
		}
	}

	opts := clientbase.ClientOpts{
		URL:      string(sec.Data[RancherKeyBaseURL]),
		Insecure: true,
	}

	opts.TokenKey = string(sec.Data[RancherKeyToken])
	rc, token, _ := decodeToken(opts)

	if string(sec.Data[RancherKeyTokenState]) == state &&
		string(sec.Data[RancherKeyTokenNext]) != "" {

		opts.TokenKey = string(sec.Data[RancherKeyTokenNext])
		nextClient, nextToken, err := decodeToken(opts)
		if err == nil && nextToken.ExpiresAt.After(token.ExpiresAt) {
			rc = nextClient
			token = nextToken
		}
	}

	if rc == nil {
		log.Info("no active rancher token found")
		os.Exit(1)
	}

	return rc, token, nil
}

func decodeToken(opts clientbase.ClientOpts) (*rancher.Client, *RancherToken, error) {
	rc, err := rancher.NewClient(&opts)
	if err != nil {
		return nil, nil, err
	}

	tokenID, _, found := strings.Cut(opts.TokenKey, ":")
	if !found {
		return nil, nil, errors.New("rancher token format is invalid")
	}

	token, err := rc.Token.ByID(tokenID)
	if err != nil {
		return nil, nil, err
	}

	var expiresAt time.Time
	if token.ExpiresAt != "" {
		expiresAt, err = time.Parse(time.RFC3339, token.ExpiresAt)
		if err != nil {
			return nil, nil, err
		}
	} else {
		expiresAt = time.Now().Add(time.Duration(token.TTLMillis) * time.Millisecond)
	}

	return rc, &RancherToken{
		UserID:    token.UserID,
		TokenID:   tokenID,
		Token:     opts.TokenKey,
		ExpiresAt: expiresAt,
	}, nil
}

func RenewToken(ctx context.Context, rc *rancher.Client, kc client.Client, sec *core.Secret, state string) (*rancher.Token, error) {
	log := log.FromContext(ctx)

	newToken, err := rc.Token.Create(&rancher.Token{
		Description: fmt.Sprintf("monitoring-operator-%d", time.Now().Unix()),
		TTLMillis:   365 * 24 * time.Hour.Milliseconds(),
	})
	if err != nil {
		return nil, err
	}
	log.Info("issued new rancher token", "tokenID", newToken.Name)

	result, err := controllerutil.CreateOrPatch(ctx, kc, sec, func() error {
		if sec.StringData == nil {
			sec.StringData = make(map[string]string)
		}
		sec.StringData[RancherKeyTokenNext] = newToken.Token
		sec.StringData[RancherKeyTokenState] = state
		sec.StringData[RancherKeyTokenExpiresAt] = newToken.ExpiresAt
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result != controllerutil.OperationResultNone {
		log.Info(string(result)+" rancher token", "tokenID", newToken.Name)
	}

	return newToken, nil
}
