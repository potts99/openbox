// SPDX-License-Identifier: AGPL-3.0-only

// Package pi models versioned owner-level Pi profiles (non-secret shared settings).
package pi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openbox-dev/openbox/internal/domain"
)

// Settings is the non-secret Pi global settings document OpenBox stores and
// later materializes into ~/.pi/agent/settings.json inside instances.
type Settings struct {
	DefaultProvider      string       `json:"defaultProvider,omitempty"`
	DefaultModel         string       `json:"defaultModel,omitempty"`
	DefaultThinkingLevel string       `json:"defaultThinkingLevel,omitempty"`
	Theme                string       `json:"theme,omitempty"`
	EnabledModels        []string     `json:"enabledModels,omitempty"`
	Packages             []PackageRef `json:"packages,omitempty"`
	Extensions           []string     `json:"extensions,omitempty"`
	Skills               []string     `json:"skills,omitempty"`
	Prompts              []string     `json:"prompts,omitempty"`
	Themes               []string     `json:"themes,omitempty"`
}

// PackageRef is a Pi package entry (string form or object form with source).
type PackageRef struct {
	Source string `json:"source,omitempty"`
}

// UnmarshalJSON accepts Pi's string or object package forms.
func (p *PackageRef) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var source string
		if err := json.Unmarshal(data, &source); err != nil {
			return err
		}
		p.Source = source
		return nil
	}
	var obj struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	p.Source = obj.Source
	return nil
}

// MarshalJSON emits the compact string form when only Source is set.
func (p PackageRef) MarshalJSON() ([]byte, error) {
	if p.Source == "" {
		return []byte(`""`), nil
	}
	return json.Marshal(p.Source)
}

// ParseSettings decodes and validates a non-secret Pi settings document.
func ParseSettings(raw []byte) (Settings, error) {
	if err := rejectSecrets(raw); err != nil {
		return Settings{}, err
	}
	var settings Settings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return Settings{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "settings", Cause: err}
	}
	if err := settings.Validate(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

// Marshal encodes settings as Pi-compatible JSON.
func (s Settings) Marshal() ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	if err := rejectSecrets(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Validate checks structural constraints on a decoded settings document.
func (s Settings) Validate() error {
	for i, pkg := range s.Packages {
		if strings.TrimSpace(pkg.Source) == "" {
			return &domain.Error{Code: domain.CodeInvalidArgument, Field: fmt.Sprintf("packages[%d]", i)}
		}
	}
	return nil
}

func rejectSecrets(raw []byte) error {
	lower := strings.ToLower(string(raw))
	forbidden := []string{
		`"apikey"`,
		`"api_key"`,
		`"refreshtoken"`,
		`"refresh_token"`,
		`"authorization"`,
		`"access_token"`,
		`"accesstoken"`,
		`"client_secret"`,
		`"clientsecret"`,
		"sk-ant-",
		"sk-proj-",
		"~/.pi/agent/auth",
	}
	for _, needle := range forbidden {
		if strings.Contains(lower, needle) {
			return &domain.Error{Code: domain.CodeInvalidArgument, Field: "settings.secret"}
		}
	}
	return nil
}
