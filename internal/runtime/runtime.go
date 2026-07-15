// SPDX-License-Identifier: AGPL-3.0-only

// Package runtime defines the narrow boundary between OpenBox and an instance runtime.
package runtime

import (
	"context"
	"errors"
	"io"
)

var (
	ErrNotFound      = errors.New("runtime resource not found")
	ErrAlreadyExists = errors.New("runtime resource already exists")
	ErrUnsupported   = errors.New("runtime operation unsupported")
)

type Capabilities struct {
	Architecture    string          `json:"architecture"`
	IncusVersion    string          `json:"incus_version"`
	Namespaces      map[string]bool `json:"namespaces"`
	Cgroups         bool            `json:"cgroups"`
	StorageDrivers  []string        `json:"storage_drivers"`
	NetworkTools    map[string]bool `json:"network_tools"`
	KVM             bool            `json:"kvm"`
	Containers      bool            `json:"containers"`
	VirtualMachines bool            `json:"virtual_machines"`
	VMAvailability  VMAvailability  `json:"vm_availability"`
	VMReason        string          `json:"vm_reason,omitempty"`
}

type VMAvailability string

const (
	VMAvailable                       VMAvailability = "supported"
	VMUnavailableKVMAbsent            VMAvailability = "kvm_absent"
	VMUnavailableKVMPermission        VMAvailability = "kvm_permission_denied"
	VMUnavailableNestedVirtualization VMAvailability = "nested_virtualization_unavailable"
	VMUnavailableIncus                VMAvailability = "incus_vm_unsupported"
)

type Image struct {
	Fingerprint  string
	Aliases      []string
	Architecture string
	Type         string
	CloudInit    bool
}

type InstanceState string

const (
	StateRunning InstanceState = "running"
	StateStopped InstanceState = "stopped"
)

type Instance struct {
	Ref        string
	Image      string
	State      InstanceState
	IsVM       bool
	Metadata   map[string]string
	Resources  Resources
	Privileged bool
	Snapshots  []string
}

type Resources struct {
	VCPUs       int
	MemoryBytes int64
	DiskBytes   int64
}

type CreateRequest struct {
	Ref, Image     string
	VM             bool
	Unprivileged   bool
	OwnerPublicKey string
	Metadata       map[string]string
	Resources      Resources
}

type ReadinessRequest struct {
	Ref   string
	Stage func(string) error
}

type ExecRequest struct {
	Ref     string
	Command []string
	Stdin   io.Reader
}

type ExecResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

type CopyRequest struct {
	SourceRef, Snapshot, TargetRef string
}

// Runtime is deliberately provider-neutral. Implementations must honor context
// cancellation and return the sentinel errors above through errors.Is.
type Runtime interface {
	DiscoverCapabilities(context.Context) (Capabilities, error)
	ListImages(context.Context) ([]Image, error)
	ListInstances(context.Context) ([]Instance, error)
	InspectInstance(context.Context, string) (Instance, error)
	CreateInstance(context.Context, CreateRequest) (Instance, error)
	StartInstance(context.Context, string) error
	WaitInstanceReady(context.Context, ReadinessRequest) error
	StopInstance(context.Context, string) error
	Exec(context.Context, ExecRequest) (ExecResult, error)
	CreateSnapshot(context.Context, string, string) error
	CopyInstance(context.Context, CopyRequest) (Instance, error)
	DeleteInstance(context.Context, string) error
}
