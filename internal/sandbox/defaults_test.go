// SPDX-License-Identifier: AGPL-3.0-only

package sandbox_test

import (
	"errors"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/images"
	"github.com/openbox-dev/openbox/internal/sandbox"
)

func TestApplyDefaultsFillsSandboxPolicy(t *testing.T) {
	t.Parallel()
	got, err := sandbox.ApplyDefaults(sandbox.CreateDefaults{
		Kind:         domain.KindSandbox,
		Architecture: "x86_64",
		Runtime:      "container",
		Catalog:      images.DefaultCatalog(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Image != "openbox:sandbox/ubuntu/24.04" {
		t.Fatalf("image = %q", got.Image)
	}
	if got.RequestedIsolation != "" {
		t.Fatalf("isolation = %q, want empty for capability resolve at create", got.RequestedIsolation)
	}
	if got.Resources != sandbox.DefaultResources() {
		t.Fatalf("resources = %+v, want %+v", got.Resources, sandbox.DefaultResources())
	}
	if got.Lifetime != domain.DefaultSandboxLifetime {
		t.Fatalf("lifetime = %v", got.Lifetime)
	}
}

func TestApplyDefaultsPreservesExplicitOverrides(t *testing.T) {
	t.Parallel()
	custom := domain.Resources{VCPUs: 4, MemoryBytes: 8 << 30, DiskBytes: 20 << 30}
	got, err := sandbox.ApplyDefaults(sandbox.CreateDefaults{
		Kind:               domain.KindSandbox,
		Architecture:       "x86_64",
		Runtime:            "container",
		Catalog:            images.DefaultCatalog(),
		Image:              "custom:image",
		RequestedIsolation: domain.IsolationStrong,
		Resources:          custom,
		Lifetime:           2 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Image != "custom:image" {
		t.Fatalf("image = %q", got.Image)
	}
	if got.RequestedIsolation != domain.IsolationStrong {
		t.Fatalf("isolation = %q", got.RequestedIsolation)
	}
	if got.Resources != custom {
		t.Fatalf("resources = %+v", got.Resources)
	}
	if got.Lifetime != 2*time.Hour {
		t.Fatalf("lifetime = %v", got.Lifetime)
	}
}

func TestApplyDefaultsRejectsLifetimeAboveMax(t *testing.T) {
	t.Parallel()
	_, err := sandbox.ApplyDefaults(sandbox.CreateDefaults{
		Kind:         domain.KindSandbox,
		Architecture: "x86_64",
		Runtime:      "container",
		Catalog:      images.DefaultCatalog(),
		Lifetime:     domain.MaxSandboxLifetime + time.Second,
	})
	if err == nil {
		t.Fatal("expected max TTL rejection")
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeInvalidArgument {
		t.Fatalf("got %v, want invalid_argument", err)
	}
}

func TestApplyDefaultsLeavesPersistentKindsWithoutLifetime(t *testing.T) {
	t.Parallel()
	for _, kind := range []domain.InstanceKind{domain.KindVPS} {
		got, err := sandbox.ApplyDefaults(sandbox.CreateDefaults{
			Kind:         kind,
			Architecture: "x86_64",
			Runtime:      "container",
			Catalog:      images.DefaultCatalog(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.Lifetime != 0 {
			t.Fatalf("%s lifetime = %v, want 0", kind, got.Lifetime)
		}
		if got.Image == "" {
			t.Fatalf("%s image default missing", kind)
		}
		if got.Resources == (domain.Resources{}) {
			t.Fatalf("%s resource default missing", kind)
		}
	}
}
