// SPDX-License-Identifier: AGPL-3.0-only

package pool

const (
	RoleLabel      = "user.openbox.pool_role"
	StateLabel     = "user.openbox.pool_state"
	ClaimedAtLabel = "user.openbox.pool_claimed_at"

	RoleGolden = "golden"
	RoleSlot   = "slot"

	StateStopped  = "stopped"
	StateRunning  = "running"
	StateClaiming = "claiming"

	GoldenRef      = "obx-pool-golden"
	GoldenSnapshot = "pool-ready"
	RefPrefix      = "obx-pool-"

	InternalOwnerID    = "openbox-pool"
	InternalInstanceID = "pool-internal"
)

// Substrate is the active warm-pool runtime shape for this host.
type Substrate string

const (
	SubstrateContainer Substrate = "container"
	SubstrateVM        Substrate = "virtual_machine"
)
