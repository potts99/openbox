// SPDX-License-Identifier: AGPL-3.0-only

// Package clones implements durable instance copy (cp) with provenance.
package clones

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

const (
	opCopy = "instance.copy"

	// WarningFullCopy is reported before execution when storage lacks CoW.
	WarningFullCopy = "storage does not provide copy-on-write; OpenBox will use a full copy and will not claim copy-on-write behavior"
	// WarningSecrets is reported when cloning an unprotected VPS that has Pi
	// (or other guest-persisted agent state) installed.
	WarningSecrets = "source has installed software that may retain secrets; cloned guest files may include them"
)

// CloneRuntime is the narrow runtime boundary used for efficient copies.
type CloneRuntime interface {
	DiscoverCapabilities(context.Context) (runtimeapi.Capabilities, error)
	CopyInstance(context.Context, runtimeapi.CopyRequest) (runtimeapi.Instance, error)
	InspectInstance(context.Context, string) (runtimeapi.Instance, error)
}

// Repository persists clone targets and durable operations.
type Repository interface {
	ListInstancesByOwner(context.Context, domain.OwnerID, int) ([]domain.Instance, error)
	GetInstance(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error)
	CreateInstance(context.Context, domain.Instance, domain.Operation) (domain.Operation, bool, error)
	UpdateInstanceObservation(context.Context, domain.OwnerID, domain.InstanceID, string, domain.IsolationType, domain.ObservedState, domain.ErrorCode, time.Time) error
	GetOperationByIdempotency(context.Context, domain.OwnerID, string) (domain.Operation, bool, error)
	CompleteOperation(context.Context, domain.OwnerID, domain.OperationID, time.Time) error
	UpdateOperationStage(context.Context, domain.OwnerID, domain.OperationID, string, int, time.Time) error
	ListInstanceSoftware(context.Context, domain.OwnerID, domain.InstanceID) ([]domain.InstanceSoftware, error)
}

// Options configures clocks and ID generation.
type Options struct {
	Now   func() time.Time
	NewID func() string
}

// Service orchestrates cp source target through durable operations.
type Service struct {
	runtime CloneRuntime
	repo    Repository
	now     func() time.Time
	newID   func() string
}

// CopyInput is the administrator-facing cp payload. Source may be a name or ID.
type CopyInput struct {
	OwnerID        domain.OwnerID
	Source         string
	Destination    string
	SnapshotID     domain.SnapshotID
	IdempotencyKey string
}

// SubmitResult is the durable copy submission plus pre-execution warnings.
type SubmitResult struct {
	Instance  domain.Instance
	Operation domain.Operation
	Warnings  []string
}

type copyPayload struct {
	SourceRef   string            `json:"source_ref"`
	SnapshotRef string            `json:"snapshot_ref"`
	TargetRef   string            `json:"target_ref"`
	SourceID    domain.InstanceID `json:"source_instance_id"`
	ImageID     domain.ImageID    `json:"source_image_id"`
	OwnerID     domain.OwnerID    `json:"owner_id"`
}

// New constructs a clone Service.
func New(runtime CloneRuntime, repo Repository, options Options) (*Service, error) {
	if runtime == nil || repo == nil {
		return nil, errors.New("runtime and repository are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = randomID
	}
	return &Service{runtime: runtime, repo: repo, now: options.Now, newID: options.NewID}, nil
}

// StorageEfficientCopy reports whether any advertised driver supports CoW copies.
// OpenBox never claims copy-on-write unless this returns true.
func StorageEfficientCopy(drivers []string) bool {
	for _, driver := range drivers {
		switch strings.ToLower(strings.TrimSpace(driver)) {
		case "btrfs", "zfs", "lvm", "ceph", "cephfs", "powerflex", "pure":
			return true
		}
	}
	return false
}

// SubmitCopy records a new instance cloned from source without mutating runtime yet.
func (s *Service) SubmitCopy(ctx context.Context, input CopyInput) (SubmitResult, error) {
	if input.OwnerID == "" || input.Source == "" || input.Destination == "" || input.IdempotencyKey == "" {
		return SubmitResult{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "copy"}
	}
	requestHash := hashString("copy:" + input.Source + ":" + input.Destination + ":" + string(input.SnapshotID))
	if existing, found, err := s.repo.GetOperationByIdempotency(ctx, input.OwnerID, input.IdempotencyKey); err != nil {
		return SubmitResult{}, err
	} else if found {
		if existing.Type != opCopy || existing.RequestHash != requestHash {
			return SubmitResult{}, &domain.Error{Code: domain.CodeIdempotencyConflict, Field: "idempotency_key"}
		}
		instance, getErr := s.repo.GetInstance(ctx, input.OwnerID, domain.InstanceID(existing.TargetID))
		return SubmitResult{Instance: instance, Operation: existing}, getErr
	}
	source, err := s.resolve(ctx, input.OwnerID, input.Source)
	if err != nil {
		return SubmitResult{}, err
	}
	if source.RuntimeRef == "" || source.DeletedAt != nil {
		return SubmitResult{}, &domain.Error{Code: domain.CodeConflict, Field: "source"}
	}
	capabilities, err := s.runtime.DiscoverCapabilities(ctx)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("discover runtime capabilities: %w", err)
	}
	warnings := make([]string, 0, 2)
	if !StorageEfficientCopy(capabilities.StorageDrivers) {
		warnings = append(warnings, WarningFullCopy)
	}
	if !source.Protected {
		rows, listErr := s.repo.ListInstanceSoftware(ctx, source.OwnerID, source.ID)
		if listErr != nil {
			return SubmitResult{}, listErr
		}
		for _, row := range rows {
			if row.PackageID == "pi" && (row.Status == domain.SoftwareInstalled || row.Status == domain.SoftwareFailed || row.Status == domain.SoftwarePending) {
				warnings = append(warnings, WarningSecrets)
				break
			}
		}
	}
	now := s.now().UTC()
	clone, err := domain.NewInstance(domain.InstanceID(s.newID()), input.OwnerID, input.Destination, source.Kind, now)
	if err != nil {
		return SubmitResult{}, err
	}
	clone.ImageID = source.ImageID
	clone.RequestedIsolation = source.RequestedIsolation
	clone.ActualIsolation = source.ActualIsolation
	clone.Resources = source.Resources
	clone.RuntimeRef = runtimeReference(clone.ID)
	clone.Protected = false
	clone.CloneSourceInstanceID = source.ID
	clone.CloneSourceSnapshotID = input.SnapshotID
	clone.CloneSourceImageID = source.ImageID
	if err := domain.ValidateInstance(clone); err != nil {
		return SubmitResult{}, err
	}
	operation := domain.Operation{
		ID: domain.OperationID(s.newID()), OwnerID: input.OwnerID, Type: opCopy, TargetType: "instance", TargetID: string(clone.ID),
		Status: domain.OperationPending, Stage: "runtime", IdempotencyKey: input.IdempotencyKey, RequestHash: requestHash,
		CreatedAt: now, UpdatedAt: now,
	}
	payload, err := json.Marshal(copyPayload{
		SourceRef: source.RuntimeRef, TargetRef: clone.RuntimeRef, SourceID: source.ID, ImageID: source.ImageID, OwnerID: input.OwnerID,
	})
	if err != nil {
		return SubmitResult{}, fmt.Errorf("encode copy payload: %w", err)
	}
	operation.PayloadJSON = payload
	recorded, replay, err := s.repo.CreateInstance(ctx, clone, operation)
	if err != nil {
		return SubmitResult{}, err
	}
	if replay {
		clone, err = s.repo.GetInstance(ctx, input.OwnerID, domain.InstanceID(recorded.TargetID))
		return SubmitResult{Instance: clone, Operation: recorded, Warnings: warnings}, err
	}
	return SubmitResult{Instance: clone, Operation: operation, Warnings: warnings}, nil
}

// RecoverOperation executes or resumes a durable copy operation.
func (s *Service) RecoverOperation(ctx context.Context, operation domain.Operation) error {
	if operation.Type != opCopy {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.type"}
	}
	clone, err := s.repo.GetInstance(ctx, operation.OwnerID, domain.InstanceID(operation.TargetID))
	if err != nil {
		return err
	}
	var payload copyPayload
	if err := json.Unmarshal(operation.PayloadJSON, &payload); err != nil || payload.SourceRef == "" || payload.TargetRef == "" {
		return &domain.Error{Code: domain.CodeConflict, Field: "operation.payload", Cause: err}
	}
	if err := s.repo.UpdateOperationStage(ctx, operation.OwnerID, operation.ID, "copying_instance", 40, s.now()); err != nil {
		return err
	}
	copied, err := s.runtime.InspectInstance(ctx, payload.TargetRef)
	if err != nil && !errors.Is(err, runtimeapi.ErrNotFound) {
		return fmt.Errorf("inspect copy target: %w", err)
	}
	if errors.Is(err, runtimeapi.ErrNotFound) {
		copied, err = s.runtime.CopyInstance(ctx, runtimeapi.CopyRequest{
			SourceRef: payload.SourceRef, Snapshot: payload.SnapshotRef, TargetRef: payload.TargetRef,
			Metadata: managedMetadata(payload.OwnerID, clone.ID),
		})
		if err != nil && !errors.Is(err, runtimeapi.ErrAlreadyExists) {
			return fmt.Errorf("copy runtime instance: %w", err)
		}
		if errors.Is(err, runtimeapi.ErrAlreadyExists) {
			copied, err = s.runtime.InspectInstance(ctx, payload.TargetRef)
			if err != nil {
				return fmt.Errorf("inspect existing copy target: %w", err)
			}
		}
	}
	if err := s.repo.UpdateOperationStage(ctx, operation.OwnerID, operation.ID, "verifying_copy", 70, s.now()); err != nil {
		return err
	}
	if err := instances.VerifyRuntimeIdentity(copied, clone.ID, clone.ActualIsolation); err != nil {
		return err
	}
	observed := domain.ObservedStopped
	if copied.State == runtimeapi.StateRunning {
		observed = domain.ObservedRunning
	}
	if err := s.repo.UpdateInstanceObservation(ctx, clone.OwnerID, clone.ID, clone.RuntimeRef, clone.ActualIsolation, observed, "", s.now()); err != nil {
		return err
	}
	if err := s.repo.UpdateOperationStage(ctx, operation.OwnerID, operation.ID, "instance_copied", 90, s.now()); err != nil {
		return err
	}
	return s.repo.CompleteOperation(ctx, operation.OwnerID, operation.ID, s.now())
}

func managedMetadata(ownerID domain.OwnerID, instanceID domain.InstanceID) map[string]string {
	return map[string]string{
		instances.MetadataManaged: "true", instances.MetadataResource: "instance",
		instances.MetadataInstanceID: string(instanceID), instances.MetadataOwnerID: string(ownerID),
	}
}

func (s *Service) resolve(ctx context.Context, owner domain.OwnerID, target string) (domain.Instance, error) {
	listed, err := s.repo.ListInstancesByOwner(ctx, owner, 1000)
	if err != nil {
		return domain.Instance{}, err
	}
	for _, instance := range listed {
		if string(instance.ID) == target || instance.Name == target {
			return instance, nil
		}
	}
	return domain.Instance{}, &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
}

func runtimeReference(id domain.InstanceID) string {
	sum := sha256.Sum256([]byte(id))
	return "obx-" + hex.EncodeToString(sum[:12])
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return strings.ToLower(hex.EncodeToString(value))
}
