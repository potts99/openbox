// SPDX-License-Identifier: AGPL-3.0-only

package domain

import (
	"regexp"
	"time"
)

const (
	DefaultSandboxLifetime = time.Hour
	MaxSandboxLifetime     = 24 * time.Hour
)

var instanceNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func ValidateInstanceName(name string) error {
	if !instanceNamePattern.MatchString(name) {
		return newError(CodeInvalidArgument, "name")
	}
	return nil
}

// NewInstance applies kind-specific defaults and validates the resulting record.
func NewInstance(id InstanceID, ownerID OwnerID, name string, kind InstanceKind, now time.Time) (Instance, error) {
	now = now.UTC()
	i := Instance{
		ID: id, OwnerID: ownerID, Name: name, Kind: kind,
		RequestedIsolation: IsolationStrong, // create path may overwrite after capability resolve
		ActualIsolation:    IsolationUnknown,
		EgressMode:         defaultEgressMode(kind),
		EgressProfileID:    DefaultEgressProfileID(kind),
		DesiredState:       DesiredRunning, ObservedState: ObservedPending,
		CreatedAt: now, UpdatedAt: now,
	}
	if kind == KindSandbox {
		expires := now.Add(DefaultSandboxLifetime)
		i.ExpiresAt = &expires
	}
	return i, ValidateInstance(i)
}

// ExtendSandboxExpiry adds duration to a Sandbox's expires_at. It rejects
// non-sandboxes, irreversible deletion, non-positive durations, and any result
// past CreatedAt+MaxSandboxLifetime.
func ExtendSandboxExpiry(i Instance, by time.Duration, now time.Time) (Instance, error) {
	if i.Kind != KindSandbox {
		return Instance{}, newError(CodeInvalidArgument, "kind")
	}
	if by <= 0 {
		return Instance{}, newError(CodeInvalidArgument, "duration")
	}
	if i.DesiredState == DesiredDeleted || i.ObservedState == ObservedDeleting || i.ObservedState == ObservedDeleted {
		return Instance{}, newError(CodeInvalidTransition, "expires_at")
	}
	if i.ExpiresAt == nil {
		return Instance{}, newError(CodeExpiryRequired, "expires_at")
	}
	next := i.ExpiresAt.Add(by)
	i.ExpiresAt = &next
	i.UpdatedAt = now.UTC()
	if err := ValidateInstance(i); err != nil {
		return Instance{}, err
	}
	return i, nil
}

func ValidateInstance(i Instance) error {
	if i.ID == "" {
		return newError(CodeInvalidArgument, "id")
	}
	if i.OwnerID == "" {
		return newError(CodeInvalidArgument, "owner_id")
	}
	if err := ValidateInstanceName(i.Name); err != nil {
		return err
	}
	switch i.Kind {
	case KindSandbox, KindVPS:
	default:
		return newError(CodeInvalidArgument, "kind")
	}
	switch i.RequestedIsolation {
	case "", IsolationStrong, IsolationContainerReq:
	default:
		return newError(CodeInvalidArgument, "requested_isolation")
	}
	switch i.ActualIsolation {
	case IsolationUnknown, IsolationContainer, IsolationVM:
	default:
		return newError(CodeInvalidArgument, "actual_isolation")
	}
	switch i.EgressMode {
	case "", EgressStandard, EgressRestricted:
	default:
		return newError(CodeInvalidArgument, "egress_mode")
	}
	switch i.DesiredState {
	case DesiredRunning, DesiredStopped, DesiredDeleted:
	default:
		return newError(CodeInvalidArgument, "desired_state")
	}
	switch i.ObservedState {
	case ObservedPending, ObservedCreating, ObservedRunning, ObservedStopping, ObservedStopped, ObservedDeleting, ObservedDeleted, ObservedError:
	default:
		return newError(CodeInvalidArgument, "observed_state")
	}
	if i.Kind == KindSandbox && i.ExpiresAt == nil {
		return newError(CodeExpiryRequired, "expires_at")
	}
	if i.ExpiresAt != nil && !i.ExpiresAt.After(i.CreatedAt) {
		return newError(CodeInvalidArgument, "expires_at")
	}
	if i.Kind == KindSandbox && i.ExpiresAt != nil && i.ExpiresAt.After(i.CreatedAt.Add(MaxSandboxLifetime)) {
		return newError(CodeInvalidArgument, "expires_at")
	}
	if i.Resources.VCPUs < 0 {
		return newError(CodeInvalidArgument, "resources.vcpus")
	}
	if i.Resources.MemoryBytes < 0 {
		return newError(CodeInvalidArgument, "resources.memory_bytes")
	}
	if i.Resources.DiskBytes < 0 {
		return newError(CodeInvalidArgument, "resources.disk_bytes")
	}
	if i.Protected && i.DesiredState == DesiredDeleted {
		return newError(CodeProtectedBase, "desired_state")
	}
	return nil
}

func defaultEgressMode(kind InstanceKind) EgressMode {
	if kind == KindSandbox {
		return EgressRestricted
	}
	return EgressStandard
}

func ValidateOperation(op Operation) error {
	if op.ID == "" || op.OwnerID == "" || op.Type == "" || op.TargetType == "" || op.TargetID == "" || op.IdempotencyKey == "" || op.RequestHash == "" {
		return newError(CodeInvalidArgument, "operation")
	}
	switch op.Status {
	case OperationPending, OperationRunning, OperationSucceeded, OperationFailed:
	default:
		return newError(CodeInvalidArgument, "operation.status")
	}
	if op.Progress < 0 || op.Progress > 100 {
		return newError(CodeInvalidArgument, "operation.progress")
	}
	return nil
}

func ValidateObservedTransition(from, to ObservedState) error {
	if from == to {
		return nil
	}
	if to == ObservedError {
		return nil
	}
	allowed := map[ObservedState]map[ObservedState]bool{
		ObservedPending: {ObservedCreating: true, ObservedDeleting: true},
		// Creating may land stopped after snapshot/clone copy without an intervening start.
		ObservedCreating: {ObservedRunning: true, ObservedStopped: true, ObservedDeleting: true},
		ObservedRunning:  {ObservedStopping: true, ObservedDeleting: true},
		ObservedStopping: {ObservedStopped: true, ObservedDeleting: true},
		ObservedStopped:  {ObservedRunning: true, ObservedDeleting: true},
		ObservedError:    {ObservedPending: true, ObservedCreating: true, ObservedRunning: true, ObservedStopping: true, ObservedStopped: true, ObservedDeleting: true},
		ObservedDeleting: {ObservedDeleted: true},
	}
	if allowed[from][to] {
		return nil
	}
	return newError(CodeInvalidTransition, "observed_state")
}

func ValidateDesiredTransition(i Instance, to DesiredState) error {
	switch to {
	case DesiredRunning, DesiredStopped, DesiredDeleted:
	default:
		return newError(CodeInvalidArgument, "desired_state")
	}
	if i.DesiredState == DesiredDeleted && to != DesiredDeleted {
		return newError(CodeInvalidTransition, "desired_state")
	}
	if i.Protected && to == DesiredDeleted {
		return newError(CodeProtectedBase, "desired_state")
	}
	return nil
}
