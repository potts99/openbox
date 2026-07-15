// SPDX-License-Identifier: AGPL-3.0-only

// Package domain defines OpenBox's resource vocabulary and lifecycle rules.
package domain

import "time"

type (
	OwnerID             string
	SSHKeyID            string
	InstanceID          string
	ImageID             string
	SnapshotID          string
	RouteID             string
	PiProfileID         string
	CredentialProfileID string
	GatewayGrantID      string
	EgressProfileID     string
	OperationID         string
	AuditEventID        string
)

type Owner struct {
	ID        OwnerID
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SSHKey struct {
	ID          SSHKeyID
	OwnerID     OwnerID
	Fingerprint string
	PublicKey   string
	Label       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type InstanceKind string

const (
	KindSandbox InstanceKind = "sandbox"
	KindVPS     InstanceKind = "vps"
	KindDevbox  InstanceKind = "devbox"
)

type IsolationRequest string

const (
	IsolationBestAvailable IsolationRequest = "best_available"
	IsolationStandard      IsolationRequest = "standard"
	IsolationStrong        IsolationRequest = "strong"
)

type IsolationType string

const (
	IsolationUnknown   IsolationType = "unknown"
	IsolationContainer IsolationType = "container"
	IsolationVM        IsolationType = "virtual_machine"
)

type DesiredState string

const (
	DesiredRunning DesiredState = "running"
	DesiredStopped DesiredState = "stopped"
	DesiredDeleted DesiredState = "deleted"
)

type ObservedState string

const (
	ObservedPending  ObservedState = "pending"
	ObservedCreating ObservedState = "creating"
	ObservedRunning  ObservedState = "running"
	ObservedStopping ObservedState = "stopping"
	ObservedStopped  ObservedState = "stopped"
	ObservedDeleting ObservedState = "deleting"
	ObservedDeleted  ObservedState = "deleted"
	ObservedError    ObservedState = "error"
)

type Resources struct {
	VCPUs       int
	MemoryBytes int64
	DiskBytes   int64
}

type Instance struct {
	ID                 InstanceID
	OwnerID            OwnerID
	Name               string
	Kind               InstanceKind
	ImageID            ImageID
	RequestedIsolation IsolationRequest
	ActualIsolation    IsolationType
	DesiredState       DesiredState
	ObservedState      ObservedState
	Resources          Resources
	ExpiresAt          *time.Time
	Protected          bool
	RuntimeRef         string
	ErrorCode          ErrorCode
	ErrorStage         string
	ErrorRetryable     bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
	DeletedAt          *time.Time
}

type Image struct {
	ID                                                 ImageID
	OwnerID                                            OwnerID
	Alias, Source, Digest, Architecture, Compatibility string
	CreatedAt, UpdatedAt                               time.Time
}

type Snapshot struct {
	ID               SnapshotID
	OwnerID          OwnerID
	InstanceID       InstanceID
	Name, RuntimeRef string
	CreatedAt        time.Time
}

type RouteVisibility string

const (
	RoutePrivate RouteVisibility = "private"
	RoutePublic  RouteVisibility = "public"
)

type Route struct {
	ID                   RouteID
	OwnerID              OwnerID
	InstanceID           InstanceID
	Hostname             string
	TargetPort           int
	Visibility           RouteVisibility
	TLSState             string
	CreatedAt, UpdatedAt time.Time
}

type PiProfile struct {
	ID                   PiProfileID
	OwnerID              OwnerID
	Name                 string
	Version              int
	SettingsJSON         []byte
	CreatedAt, UpdatedAt time.Time
}

type CredentialProfile struct {
	ID                                        CredentialProfileID
	OwnerID                                   OwnerID
	Name, Provider, GatewayStoreRef, AuthMode string
	CreatedAt, UpdatedAt                      time.Time
}

type GatewayGrant struct {
	ID                  GatewayGrantID
	OwnerID             OwnerID
	InstanceID          InstanceID
	CredentialProfileID CredentialProfileID
	Provider            string
	ExpiresAt           time.Time
	RevokedAt           *time.Time
	CreatedAt           time.Time
}

type EgressMode string

const (
	EgressStandard   EgressMode = "standard"
	EgressRestricted EgressMode = "restricted"
)

type EgressProfile struct {
	ID                      EgressProfileID
	OwnerID                 OwnerID
	Name                    string
	Mode                    EgressMode
	AllowedDestinationsJSON []byte
	DNSPolicy               string
	CreatedAt, UpdatedAt    time.Time
}

type OperationStatus string

const (
	OperationPending   OperationStatus = "pending"
	OperationRunning   OperationStatus = "running"
	OperationSucceeded OperationStatus = "succeeded"
	OperationFailed    OperationStatus = "failed"
)

type Operation struct {
	ID                   OperationID
	OwnerID              OwnerID
	Type                 string
	TargetType           string
	TargetID             string
	Status               OperationStatus
	Stage                string
	Progress             int
	ErrorCode            ErrorCode
	IdempotencyKey       string
	RequestHash          string
	PayloadJSON          []byte
	Attempts             int
	NextAttemptAt        *time.Time
	ClaimedBy            string
	ClaimToken           string
	ClaimExpiresAt       *time.Time
	ErrorClass           string
	CreatedAt, UpdatedAt time.Time
}

type OperationEvent struct {
	ID           int64
	OwnerID      OwnerID
	OperationID  OperationID
	Sequence     int
	Stage        string
	Status       OperationStatus
	ErrorClass   string
	ErrorCode    ErrorCode
	Message      string
	MetadataJSON []byte
	CreatedAt    time.Time
}

type AuditEvent struct {
	ID                                           AuditEventID
	OwnerID                                      OwnerID
	Actor, Action, TargetType, TargetID, Outcome string
	MetadataJSON                                 []byte
	CreatedAt                                    time.Time
}
