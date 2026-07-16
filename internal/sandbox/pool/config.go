// SPDX-License-Identifier: AGPL-3.0-only

package pool

import "time"

// Config controls warm-pool depth and timing.
type Config struct {
	Enabled           bool
	StoppedTarget     int
	RunningTarget     int
	ReplenishInterval time.Duration
	ClaimFenceTimeout time.Duration
	SSHReadyTimeout   time.Duration
	SSHReadyPoll      time.Duration
	ClaimTimeout      time.Duration
}

// DefaultConfig returns hybrid pool defaults for the system-container substrate.
func DefaultConfig() Config {
	return Config{
		Enabled:           true,
		StoppedTarget:     8,
		RunningTarget:     3,
		ReplenishInterval: 5 * time.Second,
		ClaimFenceTimeout: 2 * time.Minute,
		SSHReadyTimeout:   30 * time.Second,
		SSHReadyPoll:      250 * time.Millisecond,
		ClaimTimeout:      time.Second,
	}
}

// VMConfig returns conservative hybrid pool defaults for KVM VM slots.
func VMConfig() Config {
	cfg := DefaultConfig()
	cfg.StoppedTarget = 4
	cfg.RunningTarget = 2
	cfg.SSHReadyTimeout = 2 * time.Minute
	return cfg
}
