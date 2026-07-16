// SPDX-License-Identifier: AGPL-3.0-only

package pool

import (
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/sandbox"
)

// Default pool template sizing matches sandbox.DefaultResources.
const (
	DefaultVCPUs       = 2
	DefaultMemoryBytes = 2 << 30
	DefaultDiskBytes   = 10 << 30
)

// UsesDefaultResources reports whether a create request can use the warm pool.
func UsesDefaultResources(resources domain.Resources) bool {
	defaults := sandbox.DefaultResources()
	return resources == defaults
}
