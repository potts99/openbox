// SPDX-License-Identifier: AGPL-3.0-only

package pi_test

import (
	"path/filepath"
	"testing"

	pi "github.com/openbox-dev/openbox/internal/profiles/pi"
)

func TestGlobalAndProjectPathsStaySeparate(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "repo")

	global := pi.GlobalAgentDir(home)
	projectPi := pi.ProjectPiDir(project)

	if global == projectPi {
		t.Fatalf("global and project paths must differ: %q", global)
	}
	if filepath.Base(filepath.Dir(global)) != ".pi" || filepath.Base(global) != "agent" {
		t.Fatalf("global agent dir = %q, want ~/.pi/agent", global)
	}
	if filepath.Base(projectPi) != ".pi" {
		t.Fatalf("project pi dir = %q, want <project>/.pi", projectPi)
	}
	if filepath.Dir(projectPi) != project {
		t.Fatalf("project .pi escaped project root: %q vs %q", projectPi, project)
	}
}

func TestValidateProjectResourcePathRejectsTraversal(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	cases := []struct {
		name    string
		rel     string
		wantErr bool
	}{
		{name: "in tree", rel: "skills/foo", wantErr: false},
		{name: "dotdot", rel: "../outside", wantErr: true},
		{name: "absolute", rel: "/etc/passwd", wantErr: true},
		{name: "tilde auth", rel: "~/.pi/agent/auth/x", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := pi.ValidateProjectResourcePath(project, tc.rel)
			if tc.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMaterializeTargetsNeverWriteTrustOrProjectPi(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "repo")
	targets := pi.MaterializeTargets(home, project)
	if targets.GlobalSettings != filepath.Join(pi.GlobalAgentDir(home), "settings.json") {
		t.Fatalf("settings target=%q", targets.GlobalSettings)
	}
	if targets.TrustFile != "" {
		t.Fatalf("OpenBox must not target trust.json, got %q", targets.TrustFile)
	}
	if targets.ProjectSettings != "" {
		t.Fatalf("OpenBox must not materialize into project .pi, got %q", targets.ProjectSettings)
	}
}
