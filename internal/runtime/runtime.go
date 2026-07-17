// SPDX-License-Identifier: AGPL-3.0-only

// Package runtime defines the narrow boundary between OpenBox and an instance runtime.
package runtime

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
)

var (
	ErrNotFound      = errors.New("runtime resource not found")
	ErrAlreadyExists = errors.New("runtime resource already exists")
	ErrUnsupported   = errors.New("runtime operation unsupported")
	// ErrHostTarget is returned when OpenConsole is asked to address the OpenBox
	// host instead of a managed instance runtime reference.
	ErrHostTarget = errors.New("runtime console cannot target the host")
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
	Ref        string
	Command    []string
	WorkingDir string
	Env        map[string]string
	Stdin      io.Reader
}

type ExecResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// WriteFileRequest writes raw bytes into a managed guest filesystem path.
// Prefer this over Exec Stdin for any non-trivial payload (binaries, configs).
type WriteFileRequest struct {
	Ref  string
	Path string // absolute path inside the guest
	Body io.Reader
	Mode os.FileMode // guest file mode; 0 defaults to 0o644
	UID  int         // guest uid (default 0)
	GID  int         // guest gid (default 0)
}

type CopyRequest struct {
	SourceRef, Snapshot, TargetRef string
	Metadata                       map[string]string
}

// InstanceBackup is a portable runtime backup stream. Its encoding is owned by
// the runtime implementation and is intended only for ExportInstance /
// ImportInstance round trips on a compatible runtime.
type InstanceBackup struct {
	Ref  string
	Body io.Reader
}

// ConsoleRequest opens an interactive PTY inside a managed instance.
// Ref must be a runtime instance identity (never the OpenBox host).
type ConsoleRequest struct {
	Ref     string
	Command []string
	Cols    uint16
	Rows    uint16
}

// ConsoleSession is an interactive PTY attached to a managed instance.
// Implementations must not spawn a shell on the OpenBox host.
type ConsoleSession interface {
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Resize(cols, rows uint16) error
	Wait() (exitCode int, err error)
	Close() error
}

// ConsoleOpener opens interactive consoles. Runtime implementations that support
// browser terminals satisfy this narrower boundary for HTTP injection.
type ConsoleOpener interface {
	OpenConsole(context.Context, ConsoleRequest) (ConsoleSession, error)
}

// UsageSnapshot is a point-in-time reading from the runtime's instance state.
// Counter fields are cumulative; callers derive rates (CPU %, B/s) across samples.
type UsageSnapshot struct {
	Status      InstanceState
	CPUNanos    int64
	MemoryBytes int64
	DiskBytes   int64
	NetRxBytes  int64
	NetTxBytes  int64
}

// InstanceUsageReader reads live resource counters for a managed instance.
// Ref is a runtime identity (never an OpenBox host target).
type InstanceUsageReader interface {
	InstanceUsage(context.Context, string) (UsageSnapshot, error)
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
	WriteFile(context.Context, WriteFileRequest) error
	OpenConsole(context.Context, ConsoleRequest) (ConsoleSession, error)
	CreateSnapshot(context.Context, string, string) error
	DeleteSnapshot(context.Context, string, string) error
	CopyInstance(context.Context, CopyRequest) (Instance, error)
	ExportInstance(context.Context, string, io.Writer) error
	ImportInstance(context.Context, InstanceBackup) (Instance, error)
	DeleteInstance(context.Context, string) error
}

// IsHostConsoleTarget reports whether ref addresses the OpenBox host rather than
// a managed instance. Empty refs are treated as host targets.
func IsHostConsoleTarget(ref string) bool {
	switch strings.ToLower(strings.TrimSpace(ref)) {
	case "", "host":
		return true
	default:
		return false
	}
}
