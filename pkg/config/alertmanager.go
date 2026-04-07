package config

import (
	"errors"
	"fmt"
)

type AlertmanagerConfig struct {
	Email   AlertmanagerEmailConfig   `json:"email" yaml:"email"`
	Webhook AlertmanagerWebhookConfig `json:"webhook" yaml:"webhook"`
}

type SecretRef struct {
	Name string `json:"name" yaml:"name"`
	Key  string `json:"key" yaml:"key"`
}

type AlertmanagerEmailConfig struct {
	Enabled      bool      `json:"enabled" yaml:"enabled"`
	To           string    `json:"to" yaml:"to"`
	From         string    `json:"from" yaml:"from"`
	Smarthost    string    `json:"smarthost" yaml:"smarthost"`
	AuthUsername string    `json:"authUsername" yaml:"authUsername"`
	Secret       SecretRef `json:"secret" yaml:"secret"`
	RequireTLS   bool      `json:"requireTLS" yaml:"requireTLS"`
	SendResolved bool      `json:"sendResolved" yaml:"sendResolved"`
}

type AlertmanagerWebhookConfig struct {
	Enabled      bool      `json:"enabled" yaml:"enabled"`
	Secret       SecretRef `json:"secret" yaml:"secret"`
	SendResolved bool      `json:"sendResolved" yaml:"sendResolved"`
}

func DefaultAlertmanagerConfig() AlertmanagerConfig {
	return AlertmanagerConfig{
		Email: AlertmanagerEmailConfig{
			RequireTLS:   true,
			SendResolved: true,
		},
		Webhook: AlertmanagerWebhookConfig{
			SendResolved: true,
		},
	}
}

func (c AlertmanagerConfig) Validate() error {
	var errs []error
	if c.Email.Enabled {
		if c.Email.To == "" || c.Email.From == "" || c.Email.Smarthost == "" || c.Email.Secret.Name == "" || c.Email.Secret.Key == "" {
			errs = append(errs, fmt.Errorf("email notification requires to, from, smarthost, secret.name, and secret.key"))
		}
	}
	if c.Webhook.Enabled {
		if c.Webhook.Secret.Name == "" || c.Webhook.Secret.Key == "" {
			errs = append(errs, fmt.Errorf("webhook notification requires secret.name and secret.key"))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
