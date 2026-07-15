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
