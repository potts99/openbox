// SPDX-License-Identifier: AGPL-3.0-only

package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

func TestValidateInstanceName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		valid bool
	}{
		{"dev", true}, {"project-1", true}, {"a", true},
		{"", false}, {"UPPER", false}, {"-edge", false}, {"edge-", false},
		{"has space", false}, {strings.Repeat("a", 64), false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			err := domain.ValidateInstanceName(tt.name)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, error=%v", tt.valid, err)
			}
		})
	}
}

func TestNewInstanceKindDefaults(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.FixedZone("local", 3600))
	for _, kind := range []domain.InstanceKind{domain.KindSandbox, domain.KindVPS, domain.KindDevbox} {
		i, err := domain.NewInstance("instance-1", "owner-1", "project", kind, now)
		if err != nil {
			t.Fatal(err)
		}
		if i.CreatedAt.Location() != time.UTC {
			t.Fatal("timestamp is not UTC")
		}
		if kind == domain.KindSandbox && (i.ExpiresAt == nil || !i.ExpiresAt.Equal(i.CreatedAt.Add(time.Hour))) {
			t.Fatal("sandbox expiry default missing")
		}
		if kind != domain.KindSandbox && i.ExpiresAt != nil {
			t.Fatal("persistent kind unexpectedly expires")
		}
	}
}

func TestValidateInstancePolicies(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	i, _ := domain.NewInstance("instance-1", "owner-1", "sandbox", domain.KindSandbox, now)
	i.ExpiresAt = nil
	assertCode(t, domain.ValidateInstance(i), domain.CodeExpiryRequired)
	i, _ = domain.NewInstance("instance-2", "owner-1", "base", domain.KindDevbox, now)
	i.Protected = true
	assertCode(t, domain.ValidateDesiredTransition(i, domain.DesiredDeleted), domain.CodeProtectedBase)
	i.RequestedIsolation = domain.IsolationRequest("magic")
	assertCode(t, domain.ValidateInstance(i), domain.CodeInvalidArgument)
}

func TestValidateInstanceRejectsNegativeResources(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	tests := []struct {
		name      string
		resources domain.Resources
	}{
		{name: "vcpus", resources: domain.Resources{VCPUs: -1}},
		{name: "memory", resources: domain.Resources{MemoryBytes: -1}},
		{name: "disk", resources: domain.Resources{DiskBytes: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i, err := domain.NewInstance("instance-1", "owner-1", "project", domain.KindVPS, now)
			if err != nil {
				t.Fatal(err)
			}
			i.Resources = tt.resources
			assertCode(t, domain.ValidateInstance(i), domain.CodeInvalidArgument)
		})
	}
}

func TestObservedTransitions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		from, to domain.ObservedState
		valid    bool
	}{
		{domain.ObservedPending, domain.ObservedCreating, true},
		{domain.ObservedCreating, domain.ObservedRunning, true},
		{domain.ObservedRunning, domain.ObservedStopping, true},
		{domain.ObservedStopping, domain.ObservedStopped, true},
		{domain.ObservedStopped, domain.ObservedRunning, true},
		{domain.ObservedRunning, domain.ObservedDeleted, false},
		{domain.ObservedDeleted, domain.ObservedRunning, false},
		{domain.ObservedRunning, domain.ObservedError, true},
	}
	for _, tt := range tests {
		err := domain.ValidateObservedTransition(tt.from, tt.to)
		if (err == nil) != tt.valid {
			t.Fatalf("%s -> %s valid=%v error=%v", tt.from, tt.to, tt.valid, err)
		}
	}
}

func assertCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != code {
		t.Fatalf("got %v, want code %s", err, code)
	}
}
