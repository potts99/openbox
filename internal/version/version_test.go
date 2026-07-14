// SPDX-License-Identifier: AGPL-3.0-only

package version_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEntryPointsReportSameSemanticVersion(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))

	versions := make(map[string]string)
	for _, name := range []string{"openbox", "openboxd"} {
		cmd := exec.Command("go", "run", "./cmd/"+name, "--version")
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run %s --version: %v\n%s", name, err, output)
		}
		versions[name] = strings.TrimSpace(string(output))
		if !isSemanticVersion(versions[name]) {
			t.Errorf("%s returned non-semantic version %q", name, versions[name])
		}
	}

	if versions["openbox"] != versions["openboxd"] {
		t.Fatalf("version mismatch: openbox=%q openboxd=%q", versions["openbox"], versions["openboxd"])
	}
}

func isSemanticVersion(value string) bool {
	if !strings.HasPrefix(value, "v") {
		return false
	}
	core := strings.SplitN(strings.TrimPrefix(value, "v"), "-", 2)[0]
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}
