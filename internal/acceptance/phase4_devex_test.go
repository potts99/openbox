// SPDX-License-Identifier: AGPL-3.0-only

package acceptance_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestPhase4ExamplePackPresent guards the agent example pack that must stay in
// the tracked examples/ tree (docs/ is gitignored).
func TestPhase4ExamplePackPresent(t *testing.T) {
	t.Parallel()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	for _, rel := range []string{
		"examples/README.md",
		"examples/one-shot-sandbox/run.sh",
		"examples/durable-session/run.sh",
		"examples/agent-fanout/run.sh",
		"examples/agent-server/main.go",
		"pkg/openbox/client.go",
		"pkg/openbox/contract_test.go",
	} {
		path := filepath.Join(root, rel)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing Phase 4 deliverable %s: %v", rel, err)
		}
	}
}
