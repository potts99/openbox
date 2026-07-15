// SPDX-License-Identifier: AGPL-3.0-only

// Package metrics retains short-window instance usage samples for the dashboard.
package metrics

import (
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

// Sample is a derived point ready for the metrics stream and charts.
type Sample struct {
	At          time.Time
	CPUPercent  *float64
	MemoryBytes int64
	DiskBytes   int64
	NetRxBps    *float64
	NetTxBps    *float64
}

// Limits are the instance resource caps used for UI normalization.
type Limits struct {
	VCPUs       int
	MemoryBytes int64
	DiskBytes   int64
}

// Snapshot is the full ring plus limits sent on WebSocket connect.
type Snapshot struct {
	InstanceID      domain.InstanceID
	Limits          Limits
	IntervalSeconds int
	Samples         []Sample
}

// Target is a running instance the sampler should poll.
type Target struct {
	ID         domain.InstanceID
	RuntimeRef string
	Limits     Limits
}
