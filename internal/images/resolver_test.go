// SPDX-License-Identifier: AGPL-3.0-only

package images_test

import (
	"errors"
	"testing"

	"github.com/openbox-dev/openbox/internal/images"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestResolveAliasToFingerprint(t *testing.T) {
	available := []runtimeapi.Image{{Fingerprint: "sha256:one", Aliases: []string{"ubuntu"}}}
	resolved, err := images.Resolve("ubuntu", available)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Fingerprint != "sha256:one" {
		t.Fatalf("fingerprint=%q", resolved.Fingerprint)
	}
}

func TestResolveForTypePinsFingerprintIndependentOfLaterAliasMoves(t *testing.T) {
	available := []runtimeapi.Image{
		{Fingerprint: "sha256:container-old", Aliases: []string{"ubuntu"}, Type: "container"},
		{Fingerprint: "sha256:vm-old", Aliases: []string{"ubuntu"}, Type: "virtual-machine"},
	}
	pinned, err := images.ResolveForType("ubuntu", "container", available)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Fingerprint != "sha256:container-old" {
		t.Fatalf("fingerprint=%q", pinned.Fingerprint)
	}
	// Callers must persist pinned.Fingerprint. Re-resolving after the alias moves
	// must not be used to mutate an existing instance's ImageID.
	available = []runtimeapi.Image{
		{Fingerprint: "sha256:container-new", Aliases: []string{"ubuntu"}, Type: "container"},
		{Fingerprint: "sha256:vm-old", Aliases: []string{"ubuntu"}, Type: "virtual-machine"},
	}
	moved, err := images.ResolveForType("ubuntu", "container", available)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Fingerprint != "sha256:container-new" {
		t.Fatalf("moved fingerprint=%q", moved.Fingerprint)
	}
	if pinned.Fingerprint == moved.Fingerprint {
		t.Fatal("expected alias move to a new fingerprint for future creates")
	}
}

func TestResolveRejectsMissingAndAmbiguousAliases(t *testing.T) {
	available := []runtimeapi.Image{
		{Fingerprint: "sha256:one", Aliases: []string{"ubuntu"}},
		{Fingerprint: "sha256:two", Aliases: []string{"ubuntu"}},
	}
	var ambiguous *images.AmbiguousError
	if err := func() error { _, err := images.Resolve("ubuntu", available); return err }(); !errors.As(err, &ambiguous) {
		t.Fatalf("got %v", err)
	}
	var missing *images.NotFoundError
	if err := func() error { _, err := images.Resolve("missing", available); return err }(); !errors.As(err, &missing) {
		t.Fatalf("got %v", err)
	}
}
