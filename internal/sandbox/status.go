// SPDX-License-Identifier: AGPL-3.0-only

package sandbox

import (
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

// EgressLabel returns the effective host-enforced egress mode.
func EgressLabel(mode domain.EgressMode) string {
	return string(mode)
}

// RemainingLifetime returns how long until expiresAt, or 0 when already due/absent.
func RemainingLifetime(expiresAt *time.Time, now time.Time) time.Duration {
	if expiresAt == nil {
		return 0
	}
	now = now.UTC()
	if !expiresAt.After(now) {
		return 0
	}
	return expiresAt.Sub(now)
}

// FormatRemaining renders a compact countdown for CLI/UI (e.g. "45m", "2h30m", "expired").
func FormatRemaining(expiresAt *time.Time, now time.Time) string {
	if expiresAt == nil {
		return ""
	}
	remaining := RemainingLifetime(expiresAt, now)
	if remaining == 0 {
		return "expired"
	}
	hours := int(remaining.Hours())
	minutes := int(remaining.Minutes()) % 60
	seconds := int(remaining.Seconds()) % 60
	switch {
	case hours > 0 && minutes > 0:
		return formatInt(hours) + "h" + formatInt(minutes) + "m"
	case hours > 0:
		return formatInt(hours) + "h"
	case minutes > 0 && seconds > 0:
		return formatInt(minutes) + "m" + formatInt(seconds) + "s"
	case minutes > 0:
		return formatInt(minutes) + "m"
	default:
		return formatInt(seconds) + "s"
	}
}

func formatInt(n int) string {
	if n < 0 {
		n = 0
	}
	const digits = "0123456789"
	if n < 10 {
		return string(digits[n])
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	return string(buf[i:])
}
