// SPDX-License-Identifier: AGPL-3.0-only

package pi_test

import (
	"encoding/json"
	"strings"
	"testing"

	pi "github.com/openbox-dev/openbox/internal/profiles/pi"
)

func TestSettingsMarshalRoundTripCoversRequiredFields(t *testing.T) {
	t.Parallel()
	in := pi.Settings{
		DefaultProvider:      "anthropic",
		DefaultModel:         "claude-sonnet-4-20250514",
		DefaultThinkingLevel: "medium",
		Theme:                "dark",
		EnabledModels:        []string{"claude-*", "gpt-4o"},
		Packages:             []pi.PackageRef{{Source: "pi-skills"}},
		Extensions:           []string{"./ext"},
		Skills:               []string{"./skills"},
		Prompts:              []string{"./prompts"},
		Themes:               []string{"./themes"},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pi.ParseSettings(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultProvider != in.DefaultProvider || got.DefaultModel != in.DefaultModel {
		t.Fatalf("model prefs: %+v", got)
	}
	if got.Theme != "dark" || len(got.Packages) != 1 || got.Packages[0].Source != "pi-skills" {
		t.Fatalf("resources: %+v", got)
	}
	if len(got.Extensions) != 1 || len(got.Skills) != 1 || len(got.Prompts) != 1 || len(got.Themes) != 1 {
		t.Fatalf("resource lists: %+v", got)
	}
}

func TestSettingsRejectSecrets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
	}{
		{name: "apiKey field", raw: `{"apiKey":"sk-ant-secret"}`},
		{name: "nested refreshToken", raw: `{"auth":{"refreshToken":"rt_abc"}}`},
		{name: "authorization header", raw: `{"headers":{"Authorization":"Bearer x"}}`},
		{name: "auth store path", raw: `{"extensions":["~/.pi/agent/auth/tokens.json"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := pi.ParseSettings([]byte(tc.raw))
			if err == nil {
				t.Fatal("expected secret rejection")
			}
			if !strings.Contains(strings.ToLower(err.Error()), "secret") {
				t.Fatalf("error %q should mention secret", err)
			}
		})
	}
}

func TestSettingsAcceptStringPackageEntries(t *testing.T) {
	t.Parallel()
	got, err := pi.ParseSettings([]byte(`{"packages":["pi-skills","@org/ext"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Packages) != 2 || got.Packages[0].Source != "pi-skills" {
		t.Fatalf("packages=%+v", got.Packages)
	}
}
