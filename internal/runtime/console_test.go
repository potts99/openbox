// SPDX-License-Identifier: AGPL-3.0-only

package runtime_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
)

func TestOpenConsoleRejectsHostTargets(t *testing.T) {
	r := fake.New(runtimeapi.Capabilities{})
	for _, ref := range []string{"", "host", "HOST"} {
		_, err := r.OpenConsole(context.Background(), runtimeapi.ConsoleRequest{
			Ref: ref, Command: []string{"/bin/bash"}, Cols: 80, Rows: 24,
		})
		if !errors.Is(err, runtimeapi.ErrHostTarget) {
			t.Fatalf("ref %q: error = %v, want ErrHostTarget", ref, err)
		}
	}
}

func TestOpenConsoleRequiresManagedRunningInstance(t *testing.T) {
	r := fake.New(runtimeapi.Capabilities{})
	r.AddImage(runtimeapi.Image{Fingerprint: "sha256:base"})
	if _, err := r.CreateInstance(context.Background(), runtimeapi.CreateRequest{Ref: "dev", Image: "sha256:base"}); err != nil {
		t.Fatal(err)
	}

	_, err := r.OpenConsole(context.Background(), runtimeapi.ConsoleRequest{
		Ref: "missing", Command: []string{"/bin/sh"}, Cols: 80, Rows: 24,
	})
	if !errors.Is(err, runtimeapi.ErrNotFound) {
		t.Fatalf("missing ref error = %v", err)
	}

	_, err = r.OpenConsole(context.Background(), runtimeapi.ConsoleRequest{
		Ref: "dev", Command: []string{"/bin/sh"}, Cols: 80, Rows: 24,
	})
	if !errors.Is(err, runtimeapi.ErrUnsupported) {
		t.Fatalf("stopped instance error = %v, want ErrUnsupported", err)
	}
}

func TestOpenConsoleExposesInteractiveStreamsResizeAndWait(t *testing.T) {
	r := fake.New(runtimeapi.Capabilities{})
	r.AddImage(runtimeapi.Image{Fingerprint: "sha256:base"})
	if _, err := r.CreateInstance(context.Background(), runtimeapi.CreateRequest{Ref: "dev", Image: "sha256:base"}); err != nil {
		t.Fatal(err)
	}
	if err := r.StartInstance(context.Background(), "dev"); err != nil {
		t.Fatal(err)
	}

	session, err := r.OpenConsole(context.Background(), runtimeapi.ConsoleRequest{
		Ref: "dev", Command: []string{"/bin/bash"}, Cols: 120, Rows: 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	if err := session.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	if cols, rows := r.LastConsoleSize("dev"); cols != 100 || rows != 30 {
		t.Fatalf("last size = %dx%d", cols, rows)
	}

	written := []byte("hello-pty")
	if _, err := session.Stdin().Write(written); err != nil {
		t.Fatal(err)
	}
	_ = session.Stdin().Close()

	got := make([]byte, len(written))
	if _, err := io.ReadFull(session.Stdout(), got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(written) {
		t.Fatalf("stdout = %q, want echo of stdin", got)
	}

	done := make(chan error, 1)
	go func() { _, err := session.Wait(); done <- err }()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not return after close")
	}

	calls := r.Calls()
	found := false
	for _, call := range calls {
		if call == "console.open" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("calls = %v, want console.open", calls)
	}
	if got := r.LastConsoleRef(); got != "dev" {
		t.Fatalf("LastConsoleRef = %q", got)
	}
}
