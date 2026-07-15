// SPDX-License-Identifier: AGPL-3.0-only

package sandbox_test

import (
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/sandbox"
)

func TestRemainingAndEgressLabels(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if sandbox.EgressLabel(domain.KindSandbox) != "default" {
		t.Fatal("egress label")
	}
	future := now.Add(90 * time.Minute)
	if got := sandbox.FormatRemaining(&future, now); got != "1h30m" {
		t.Fatalf("remaining=%q", got)
	}
	past := now.Add(-time.Second)
	if got := sandbox.FormatRemaining(&past, now); got != "expired" {
		t.Fatalf("expired=%q", got)
	}
	if sandbox.FormatRemaining(nil, now) != "" {
		t.Fatal("nil expiry")
	}
}
