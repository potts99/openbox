// SPDX-License-Identifier: AGPL-3.0-only

package daemonlock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestTryAcquireExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openboxd.lock")
	first, err := TryAcquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	second, err := TryAcquire(path)
	if !errors.Is(err, ErrHeld) || second != nil {
		t.Fatalf("second=%v err=%v", second, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := TryAcquire(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = third.Close()
}
