// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"errors"
	"testing"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestOpenConsoleRejectsHostAndStubsInteractive(t *testing.T) {
	adapter, err := New(Options{SocketPath: "/tmp/openbox-incus-console-test.socket"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"", "host"} {
		_, err := adapter.OpenConsole(context.Background(), runtimeapi.ConsoleRequest{Ref: ref, Cols: 80, Rows: 24})
		if !errors.Is(err, runtimeapi.ErrHostTarget) {
			t.Fatalf("ref %q: %v", ref, err)
		}
	}
	_, err = adapter.OpenConsole(context.Background(), runtimeapi.ConsoleRequest{Ref: "obx-managed", Cols: 80, Rows: 24})
	if !errors.Is(err, runtimeapi.ErrUnsupported) {
		t.Fatalf("managed ref error = %v, want ErrUnsupported", err)
	}
}
