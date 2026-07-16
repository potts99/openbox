// SPDX-License-Identifier: AGPL-3.0-only

package pool

import (
	"context"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// Runtime is the narrow Incus surface the warm pool needs.
type Runtime interface {
	DiscoverCapabilities(context.Context) (runtimeapi.Capabilities, error)
	// StoragePoolDriver returns the driver of the configured Incus storage pool
	// (for example "zfs" or "dir"). Warm-pool CoW requires the pool itself to
	// be ZFS, not merely that the Incus daemon supports the zfs driver.
	StoragePoolDriver(context.Context) (string, error)
	ListInstances(context.Context) ([]runtimeapi.Instance, error)
	ListImages(context.Context) ([]runtimeapi.Image, error)
	InspectInstance(context.Context, string) (runtimeapi.Instance, error)
	CreatePoolContainer(context.Context, PoolCreateRequest) (runtimeapi.Instance, error)
	CopyInstance(context.Context, runtimeapi.CopyRequest) (runtimeapi.Instance, error)
	StartInstance(context.Context, string) error
	StopInstance(context.Context, string) error
	DeleteInstance(context.Context, string) error
	CreateSnapshot(context.Context, string, string) error
	DeleteSnapshot(context.Context, string, string) error
	UpdateInstanceConfig(context.Context, string, map[string]string) error
	RenameInstance(context.Context, string, string) error
	EnableBootstrapEgress(context.Context, string) error
	WaitSSHReady(context.Context, string) error
	WriteFile(context.Context, runtimeapi.WriteFileRequest) error
}

// PoolCreateRequest creates an internal pool instance without user ownership.
type PoolCreateRequest struct {
	Ref            string
	Image          string
	OwnerPublicKey string
	Metadata       map[string]string
	VM             bool // true for KVM virtual-machine golden/slots
}
