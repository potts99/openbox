// SPDX-License-Identifier: AGPL-3.0-only

// Package client provides the versioned OpenBox HTTP API client used by the CLI.
package client

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	APIVersionV1     = "v1"
	APIVersionHeader = "X-OpenBox-API-Version"
)

type Health struct {
	Status        string `json:"status"`
	APIVersion    string `json:"api_version"`
	ServerVersion string `json:"server_version"`
}

func (h Health) validate() error {
	if err := h.validateStatus(); err != nil {
		return err
	}
	if h.APIVersion != APIVersionV1 {
		return fmt.Errorf("health: unknown api_version %q", h.APIVersion)
	}
	return nil
}

func (h Health) validateStatus() error {
	if h.Status != "ok" {
		return fmt.Errorf("health: unknown status %q", h.Status)
	}
	return nil
}

type Capabilities struct {
	Architecture    string          `json:"architecture"`
	IncusVersion    string          `json:"incus_version"`
	Containers      bool            `json:"containers"`
	VirtualMachines bool            `json:"virtual_machines"`
	KVM             bool            `json:"kvm"`
	VMAvailability  string          `json:"vm_availability,omitempty"`
	Namespaces      map[string]bool `json:"namespaces,omitempty"`
	Cgroups         bool            `json:"cgroups"`
	StorageDrivers  []string        `json:"storage_drivers,omitempty"`
	NetworkTools    map[string]bool `json:"network_tools,omitempty"`
	VMReason        string          `json:"vm_reason,omitempty"`
}

func (c Capabilities) validate() error {
	if !oneOf(c.VMAvailability, "supported", "kvm_absent", "kvm_permission_denied", "nested_virtualization_unavailable", "incus_vm_unsupported") {
		return fmt.Errorf("capabilities: unknown vm_availability %q", c.VMAvailability)
	}
	return nil
}

type Resources struct {
	VCPUs       int   `json:"vcpus"`
	MemoryBytes int64 `json:"memory_bytes"`
	DiskBytes   int64 `json:"disk_bytes"`
}

type AllowlistResolution struct {
	State    string   `json:"state"`
	Pending  []string `json:"pending"`
	Resolved []string `json:"resolved"`
	Failed   []string `json:"failed"`
}

type NetworkPolicyStatus struct {
	EgressMode  string              `json:"egress_mode"`
	ACLs        []string            `json:"acls"`
	Resolution  AllowlistResolution `json:"resolution"`
	DeniedFlows uint64              `json:"denied_flows"`
}

type EgressProfile struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	Mode                  string    `json:"mode"`
	AllowedDestinations   []string  `json:"allowed_destinations"`
	System                bool      `json:"system"`
	AttachedInstanceCount int       `json:"attached_instance_count,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type Instance struct {
	ID                    string              `json:"id"`
	Name                  string              `json:"name"`
	Kind                  string              `json:"kind"`
	ImageID               string              `json:"image_id"`
	RequestedIsolation    string              `json:"requested_isolation"`
	ActualIsolation       string              `json:"actual_isolation"`
	DesiredState          string              `json:"desired_state"`
	ObservedState         string              `json:"observed_state"`
	Resources             Resources           `json:"resources"`
	ExpiresAt             *time.Time          `json:"expires_at,omitempty"`
	Protected             bool                `json:"protected"`
	ErrorCode             string              `json:"error_code,omitempty"`
	ErrorStage            string              `json:"error_stage,omitempty"`
	ErrorRetryable        bool                `json:"error_retryable,omitempty"`
	EgressProfileID       string              `json:"egress_profile_id,omitempty"`
	CloneSourceInstanceID string              `json:"clone_source_instance_id,omitempty"`
	CloneSourceSnapshotID string              `json:"clone_source_snapshot_id,omitempty"`
	CloneSourceImageID    string              `json:"clone_source_image_id,omitempty"`
	NetworkPolicy         NetworkPolicyStatus `json:"network_policy"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
}

func (i Instance) validate() error {
	if !oneOf(i.Kind, "sandbox", "vps") {
		return fmt.Errorf("instance %q: unknown kind %q", i.ID, i.Kind)
	}
	if !oneOf(i.ActualIsolation, "unknown", "container", "virtual_machine") {
		return fmt.Errorf("instance %q: unknown actual_isolation %q", i.ID, i.ActualIsolation)
	}
	if !oneOf(i.RequestedIsolation, "strong", "container") {
		return fmt.Errorf("instance %q: unknown requested_isolation %q", i.ID, i.RequestedIsolation)
	}
	if !oneOf(i.DesiredState, "running", "stopped", "deleted") {
		return fmt.Errorf("instance %q: unknown desired_state %q", i.ID, i.DesiredState)
	}
	if !oneOf(i.ObservedState, "pending", "creating", "running", "stopping", "stopped", "deleting", "deleted", "error") {
		return fmt.Errorf("instance %q: unknown observed_state %q", i.ID, i.ObservedState)
	}
	return nil
}

type CreateInstanceRequest struct {
	Name               string    `json:"name"`
	Kind               string    `json:"kind"`
	Image              string    `json:"image"`
	RequestedIsolation string    `json:"requested_isolation,omitempty"`
	Resources          Resources `json:"resources"`
	OwnerPublicKey     string    `json:"owner_public_key,omitempty"`
	LifetimeSeconds    int       `json:"lifetime_seconds,omitempty"`
	EgressProfileID    string    `json:"egress_profile_id,omitempty"`
}

type Snapshot struct {
	ID         string    `json:"id"`
	InstanceID string    `json:"instance_id"`
	Name       string    `json:"name"`
	Ready      bool      `json:"ready"`
	CreatedAt  time.Time `json:"created_at"`
}

type CreateSnapshotRequest struct {
	Name string `json:"name"`
}

type CreateSnapshotResult struct {
	Snapshot  *Snapshot `json:"snapshot,omitempty"`
	Operation Operation `json:"operation"`
}

type CloneInstanceRequest struct {
	Name           string `json:"name"`
	OwnerPublicKey string `json:"owner_public_key"`
}

type RestoreSnapshotRequest struct {
	Name           string `json:"name"`
	OwnerPublicKey string `json:"owner_public_key"`
}

type DeriveInstanceResult struct {
	Instance          *Instance `json:"instance,omitempty"`
	Operation         Operation `json:"operation"`
	Warnings          []string  `json:"warnings,omitempty"`
	StorageEfficiency string    `json:"storage_efficiency"`
}

type ExecInstanceRequest struct {
	Argv           []string          `json:"argv"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	StdinBase64    string            `json:"stdin_base64,omitempty"`
}

type ExtendInstanceRequest struct {
	DurationSeconds int `json:"duration_seconds"`
}

type OperationStatus string

const (
	OperationPending   OperationStatus = "pending"
	OperationRunning   OperationStatus = "running"
	OperationSucceeded OperationStatus = "succeeded"
	OperationFailed    OperationStatus = "failed"
)

type Operation struct {
	ID            string          `json:"id"`
	Type          string          `json:"type,omitempty"`
	TargetType    string          `json:"target_type,omitempty"`
	TargetID      string          `json:"target_id,omitempty"`
	Status        OperationStatus `json:"status"`
	Stage         string          `json:"stage,omitempty"`
	Progress      int             `json:"progress,omitempty"`
	ErrorCode     string          `json:"error_code,omitempty"`
	ErrorClass    string          `json:"error_class,omitempty"`
	Attempts      int             `json:"attempts,omitempty"`
	NextAttemptAt *time.Time      `json:"next_attempt_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func (o Operation) validate() error {
	if !oneOf(string(o.Status), string(OperationPending), string(OperationRunning), string(OperationSucceeded), string(OperationFailed)) {
		return fmt.Errorf("operation %q: unknown status %q", o.ID, o.Status)
	}
	return nil
}

func (o Operation) Terminal() bool {
	return o.Status == OperationSucceeded || o.Status == OperationFailed
}

type OperationEvent struct {
	Sequence    int             `json:"sequence"`
	OperationID string          `json:"operation_id"`
	Status      OperationStatus `json:"status"`
	Stage       string          `json:"stage,omitempty"`
	Progress    int             `json:"progress,omitempty"`
	ErrorCode   string          `json:"error_code,omitempty"`
	Message     string          `json:"message,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

func (e OperationEvent) validate() error {
	return Operation{ID: e.OperationID, Status: e.Status}.validate()
}

type MutationResult struct {
	Instance  *Instance `json:"instance,omitempty"`
	Operation Operation `json:"operation"`
}

type Route struct {
	ID         string    `json:"id"`
	InstanceID string    `json:"instance_id"`
	Hostname   string    `json:"hostname"`
	TargetPort int       `json:"target_port"`
	Visibility string    `json:"visibility"`
	TLSState   string    `json:"tls_state"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (r Route) validate() error {
	if !oneOf(r.Visibility, "private", "public") {
		return fmt.Errorf("route %q: unknown visibility %q", r.ID, r.Visibility)
	}
	return nil
}

type CreateRouteRequest struct {
	InstanceID string `json:"instance_id"`
	Hostname   string `json:"hostname"`
	TargetPort int    `json:"target_port"`
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
