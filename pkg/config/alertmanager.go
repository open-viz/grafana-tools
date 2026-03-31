package config

import (
	"errors"
	"fmt"
)

type AlertmanagerConfig struct {
	Email   AlertmanagerEmailConfig   `json:"email" yaml:"email"`
	Webhook AlertmanagerWebhookConfig `json:"webhook" yaml:"webhook"`
}

type AlertmanagerEmailConfig struct {
	Enabled        bool   `json:"enabled" yaml:"enabled"`
	To             string `json:"to" yaml:"to"`
	From           string `json:"from" yaml:"from"`
	Smarthost      string `json:"smarthost" yaml:"smarthost"`
	AuthUsername   string `json:"authUsername" yaml:"authUsername"`
	AuthSecretName string `json:"authSecretName" yaml:"authSecretName"`
	AuthSecretKey  string `json:"authSecretKey" yaml:"authSecretKey"`
	RequireTLS     bool   `json:"requireTLS" yaml:"requireTLS"`
	SendResolved   bool   `json:"sendResolved" yaml:"sendResolved"`
}

type AlertmanagerWebhookConfig struct {
	Enabled       bool   `json:"enabled" yaml:"enabled"`
	URLSecretName string `json:"urlSecretName" yaml:"urlSecretName"`
	URLSecretKey  string `json:"urlSecretKey" yaml:"urlSecretKey"`
	SendResolved  bool   `json:"sendResolved" yaml:"sendResolved"`
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
		if c.Email.To == "" || c.Email.From == "" || c.Email.Smarthost == "" || c.Email.AuthSecretName == "" || c.Email.AuthSecretKey == "" {
			errs = append(errs, fmt.Errorf("email notification requires to, from, smarthost, authSecretName, and authSecretKey"))
		}
	}
	if c.Webhook.Enabled {
		if c.Webhook.URLSecretName == "" || c.Webhook.URLSecretKey == "" {
			errs = append(errs, fmt.Errorf("webhook notification requires urlSecretName and urlSecretKey"))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
