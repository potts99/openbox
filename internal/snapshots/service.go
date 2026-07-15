// SPDX-License-Identifier: AGPL-3.0-only

// Package snapshots manages durable instance snapshots and restore-as-new copies.
package snapshots

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
	opCreate  = "snapshot.create"
	opDelete  = "snapshot.delete"
	opRestore = "snapshot.restore"
)

// SnapshotRuntime is the narrow runtime boundary used for snapshot lifecycle.
type SnapshotRuntime interface {
	CreateSnapshot(context.Context, string, string) error
	DeleteSnapshot(context.Context, string, string) error
	CopyInstance(context.Context, runtimeapi.CopyRequest) (runtimeapi.Instance, error)
	InspectInstance(context.Context, string) (runtimeapi.Instance, error)
}

// Repository persists snapshots, instances, and durable operations.
type Repository interface {
	GetInstance(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error)
	CreateInstance(context.Context, domain.Instance, domain.Operation) (domain.Operation, bool, error)
	UpdateInstanceObservation(context.Context, domain.OwnerID, domain.InstanceID, string, domain.IsolationType, domain.ObservedState, domain.ErrorCode, time.Time) error
	CreateSnapshotRecord(context.Context, domain.Snapshot, domain.Operation) (domain.Operation, bool, error)
	GetSnapshot(context.Context, domain.OwnerID, domain.SnapshotID) (domain.Snapshot, error)
	ListSnapshots(context.Context, domain.OwnerID, domain.InstanceID) ([]domain.Snapshot, error)
	DeleteSnapshotRecord(context.Context, domain.OwnerID, domain.SnapshotID) error
	UpdateSnapshotRuntimeRef(context.Context, domain.OwnerID, domain.SnapshotID, string, time.Time) error
	GetOperationByIdempotency(context.Context, domain.OwnerID, string) (domain.Operation, bool, error)
	CreateDeleteOperation(context.Context, domain.Operation) (domain.Operation, bool, error)
	CompleteOperation(context.Context, domain.OwnerID, domain.OperationID, time.Time) error
	UpdateOperationStage(context.Context, domain.OwnerID, domain.OperationID, string, int, time.Time) error
}

// Options configures clocks and ID generation for Service.
type Options struct {
	Now   func() time.Time
	NewID func() string
}

// Service orchestrates snapshot create/list/inspect/delete/restore via durable ops.
type Service struct {
	runtime SnapshotRuntime
	repo    Repository
	now     func() time.Time
	newID   func() string
}

// CreateInput records a named snapshot against an owned instance.
type CreateInput struct {
	OwnerID        domain.OwnerID
	InstanceID     domain.InstanceID
	Name           string
	IdempotencyKey string
}

// RestoreInput creates a new independent instance from a snapshot.
type RestoreInput struct {
	OwnerID        domain.OwnerID
	SnapshotID     domain.SnapshotID
	Name           string
	IdempotencyKey string
}

type restorePayload struct {
	SnapshotID     domain.SnapshotID `json:"snapshot_id"`
	SourceRef      string            `json:"source_ref"`
	SnapshotRef    string            `json:"snapshot_ref"`
	TargetRef      string            `json:"target_ref"`
	SourceInstance domain.InstanceID `json:"source_instance_id"`
}

// New constructs a snapshot Service.
func New(runtime SnapshotRuntime, repo Repository, options Options) (*Service, error) {
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

// Create durably records a snapshot and returns a pending create operation.
func (s *Service) Create(ctx context.Context, input CreateInput) (domain.Snapshot, domain.Operation, error) {
	if input.OwnerID == "" || input.InstanceID == "" || input.Name == "" || input.IdempotencyKey == "" {
		return domain.Snapshot{}, domain.Operation{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "create"}
	}
	if err := domain.ValidateInstanceName(input.Name); err != nil {
		return domain.Snapshot{}, domain.Operation{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "name"}
	}
	requestHash := hashString("create:" + string(input.InstanceID) + ":" + input.Name)
	if existing, found, err := s.repo.GetOperationByIdempotency(ctx, input.OwnerID, input.IdempotencyKey); err != nil {
		return domain.Snapshot{}, domain.Operation{}, err
	} else if found {
		if existing.Type != opCreate || existing.RequestHash != requestHash {
			return domain.Snapshot{}, domain.Operation{}, &domain.Error{Code: domain.CodeIdempotencyConflict, Field: "idempotency_key"}
		}
		snapshot, getErr := s.repo.GetSnapshot(ctx, input.OwnerID, domain.SnapshotID(existing.TargetID))
		return snapshot, existing, getErr
	}
	instance, err := s.repo.GetInstance(ctx, input.OwnerID, input.InstanceID)
	if err != nil {
		return domain.Snapshot{}, domain.Operation{}, err
	}
	if instance.RuntimeRef == "" || instance.DeletedAt != nil {
		return domain.Snapshot{}, domain.Operation{}, &domain.Error{Code: domain.CodeConflict, Field: "instance"}
	}
	now := s.now().UTC()
	snapshot := domain.Snapshot{
		ID:         domain.SnapshotID(s.newID()),
		OwnerID:    input.OwnerID,
		InstanceID: input.InstanceID,
		Name:       input.Name,
		RuntimeRef: input.Name,
		CreatedAt:  now,
	}
	operation := s.operation(opCreate, "snapshot", string(snapshot.ID), input.OwnerID, input.IdempotencyKey, requestHash)
	recorded, replay, err := s.repo.CreateSnapshotRecord(ctx, snapshot, operation)
	if err != nil {
		return domain.Snapshot{}, domain.Operation{}, err
	}
	if replay {
		snapshot, err = s.repo.GetSnapshot(ctx, input.OwnerID, domain.SnapshotID(recorded.TargetID))
		return snapshot, recorded, err
	}
	return snapshot, operation, nil
}

// Get returns one owner-scoped snapshot.
func (s *Service) Get(ctx context.Context, ownerID domain.OwnerID, id domain.SnapshotID) (domain.Snapshot, error) {
	return s.repo.GetSnapshot(ctx, ownerID, id)
}

// List returns snapshots for an owned instance.
func (s *Service) List(ctx context.Context, ownerID domain.OwnerID, instanceID domain.InstanceID) ([]domain.Snapshot, error) {
	if _, err := s.repo.GetInstance(ctx, ownerID, instanceID); err != nil {
		return nil, err
	}
	return s.repo.ListSnapshots(ctx, ownerID, instanceID)
}

// Delete records a durable delete operation for a snapshot.
func (s *Service) Delete(ctx context.Context, ownerID domain.OwnerID, id domain.SnapshotID, key string) (domain.Operation, error) {
	if ownerID == "" || id == "" || key == "" {
		return domain.Operation{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "delete"}
	}
	requestHash := hashString("delete:" + string(id))
	if existing, found, err := s.repo.GetOperationByIdempotency(ctx, ownerID, key); err != nil {
		return domain.Operation{}, err
	} else if found {
		if existing.Type != opDelete || existing.TargetID != string(id) || existing.RequestHash != requestHash {
			return domain.Operation{}, &domain.Error{Code: domain.CodeIdempotencyConflict, Field: "idempotency_key"}
		}
		return existing, nil
	}
	if _, err := s.repo.GetSnapshot(ctx, ownerID, id); err != nil {
		return domain.Operation{}, err
	}
	operation := s.operation(opDelete, "snapshot", string(id), ownerID, key, requestHash)
	recorded, _, err := s.repo.CreateDeleteOperation(ctx, operation)
	return recorded, err
}

// RestoreAsNew records a new instance derived from a snapshot.
func (s *Service) RestoreAsNew(ctx context.Context, input RestoreInput) (domain.Instance, domain.Operation, error) {
	if input.OwnerID == "" || input.SnapshotID == "" || input.Name == "" || input.IdempotencyKey == "" {
		return domain.Instance{}, domain.Operation{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "restore"}
	}
	requestHash := hashString("restore:" + string(input.SnapshotID) + ":" + input.Name)
	if existing, found, err := s.repo.GetOperationByIdempotency(ctx, input.OwnerID, input.IdempotencyKey); err != nil {
		return domain.Instance{}, domain.Operation{}, err
	} else if found {
		if existing.Type != opRestore || existing.RequestHash != requestHash {
			return domain.Instance{}, domain.Operation{}, &domain.Error{Code: domain.CodeIdempotencyConflict, Field: "idempotency_key"}
		}
		instance, getErr := s.repo.GetInstance(ctx, input.OwnerID, domain.InstanceID(existing.TargetID))
		return instance, existing, getErr
	}
	snapshot, err := s.repo.GetSnapshot(ctx, input.OwnerID, input.SnapshotID)
	if err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	source, err := s.repo.GetInstance(ctx, input.OwnerID, snapshot.InstanceID)
	if err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	now := s.now().UTC()
	clone, err := domain.NewInstance(domain.InstanceID(s.newID()), input.OwnerID, input.Name, source.Kind, now)
	if err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	clone.ImageID = source.ImageID
	clone.RequestedIsolation = source.RequestedIsolation
	clone.ActualIsolation = source.ActualIsolation
	clone.Resources = source.Resources
	clone.RuntimeRef = runtimeReference(clone.ID)
	clone.Protected = false
	if err := domain.ValidateInstance(clone); err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	operation := s.operation(opRestore, "instance", string(clone.ID), input.OwnerID, input.IdempotencyKey, requestHash)
	payload, err := json.Marshal(restorePayload{
		SnapshotID: snapshot.ID, SourceRef: source.RuntimeRef, SnapshotRef: snapshot.RuntimeRef,
		TargetRef: clone.RuntimeRef, SourceInstance: source.ID,
	})
	if err != nil {
		return domain.Instance{}, domain.Operation{}, fmt.Errorf("encode restore payload: %w", err)
	}
	operation.PayloadJSON = payload
	recorded, replay, err := s.repo.CreateInstance(ctx, clone, operation)
	if err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	if replay {
		clone, err = s.repo.GetInstance(ctx, input.OwnerID, domain.InstanceID(recorded.TargetID))
		return clone, recorded, err
	}
	return clone, operation, nil
}

// RecoverOperation executes or resumes a durable snapshot operation.
func (s *Service) RecoverOperation(ctx context.Context, operation domain.Operation) error {
	switch operation.Type {
	case opCreate:
		return s.recoverCreate(ctx, operation)
	case opDelete:
		return s.recoverDelete(ctx, operation)
	case opRestore:
		return s.recoverRestore(ctx, operation)
	default:
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.type"}
	}
}

func (s *Service) recoverCreate(ctx context.Context, operation domain.Operation) error {
	snapshot, err := s.repo.GetSnapshot(ctx, operation.OwnerID, domain.SnapshotID(operation.TargetID))
	if err != nil {
		return err
	}
	instance, err := s.repo.GetInstance(ctx, operation.OwnerID, snapshot.InstanceID)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateOperationStage(ctx, operation.OwnerID, operation.ID, "creating_snapshot", 40, s.now()); err != nil {
		return err
	}
	if err := s.runtime.CreateSnapshot(ctx, instance.RuntimeRef, snapshot.Name); err != nil && !errors.Is(err, runtimeapi.ErrAlreadyExists) {
		return fmt.Errorf("create runtime snapshot: %w", err)
	}
	if err := s.repo.UpdateSnapshotRuntimeRef(ctx, operation.OwnerID, snapshot.ID, snapshot.Name, s.now()); err != nil {
		return err
	}
	if err := s.repo.UpdateOperationStage(ctx, operation.OwnerID, operation.ID, "snapshot_created", 80, s.now()); err != nil {
		return err
	}
	return s.repo.CompleteOperation(ctx, operation.OwnerID, operation.ID, s.now())
}

func (s *Service) recoverDelete(ctx context.Context, operation domain.Operation) error {
	snapshot, err := s.repo.GetSnapshot(ctx, operation.OwnerID, domain.SnapshotID(operation.TargetID))
	if err != nil {
		if domainErr, ok := err.(*domain.Error); ok && domainErr.Code == domain.CodeNotFound {
			return s.repo.CompleteOperation(ctx, operation.OwnerID, operation.ID, s.now())
		}
		return err
	}
	instance, err := s.repo.GetInstance(ctx, operation.OwnerID, snapshot.InstanceID)
	if err != nil && !isNotFound(err) {
		return err
	}
	if err := s.repo.UpdateOperationStage(ctx, operation.OwnerID, operation.ID, "deleting_snapshot", 40, s.now()); err != nil {
		return err
	}
	if instance.RuntimeRef != "" {
		if delErr := s.runtime.DeleteSnapshot(ctx, instance.RuntimeRef, snapshot.RuntimeRef); delErr != nil && !errors.Is(delErr, runtimeapi.ErrNotFound) {
			return fmt.Errorf("delete runtime snapshot: %w", delErr)
		}
	}
	if err := s.repo.DeleteSnapshotRecord(ctx, operation.OwnerID, snapshot.ID); err != nil && !isNotFound(err) {
		return err
	}
	return s.repo.CompleteOperation(ctx, operation.OwnerID, operation.ID, s.now())
}

func (s *Service) recoverRestore(ctx context.Context, operation domain.Operation) error {
	clone, err := s.repo.GetInstance(ctx, operation.OwnerID, domain.InstanceID(operation.TargetID))
	if err != nil {
		return err
	}
	var payload restorePayload
	if err := json.Unmarshal(operation.PayloadJSON, &payload); err != nil || payload.SourceRef == "" || payload.TargetRef == "" {
		return &domain.Error{Code: domain.CodeConflict, Field: "operation.payload", Cause: err}
	}
	if err := s.repo.UpdateOperationStage(ctx, operation.OwnerID, operation.ID, "copying_instance", 40, s.now()); err != nil {
		return err
	}
	copied, err := s.runtime.InspectInstance(ctx, payload.TargetRef)
	if err != nil && !errors.Is(err, runtimeapi.ErrNotFound) {
		return fmt.Errorf("inspect restore target: %w", err)
	}
	if errors.Is(err, runtimeapi.ErrNotFound) {
		copied, err = s.runtime.CopyInstance(ctx, runtimeapi.CopyRequest{
			SourceRef: payload.SourceRef, Snapshot: payload.SnapshotRef, TargetRef: payload.TargetRef,
			Metadata: map[string]string{
				instances.MetadataManaged: "true", instances.MetadataResource: "instance",
				instances.MetadataInstanceID: string(clone.ID), instances.MetadataOwnerID: string(clone.OwnerID),
			},
		})
		if err != nil && !errors.Is(err, runtimeapi.ErrAlreadyExists) {
			return fmt.Errorf("copy instance from snapshot: %w", err)
		}
		if errors.Is(err, runtimeapi.ErrAlreadyExists) {
			copied, err = s.runtime.InspectInstance(ctx, payload.TargetRef)
			if err != nil {
				return fmt.Errorf("inspect existing restore target: %w", err)
			}
		}
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
	if err := s.repo.UpdateOperationStage(ctx, operation.OwnerID, operation.ID, "instance_copied", 80, s.now()); err != nil {
		return err
	}
	return s.repo.CompleteOperation(ctx, operation.OwnerID, operation.ID, s.now())
}

func (s *Service) operation(kind, targetType, targetID string, owner domain.OwnerID, key, hash string) domain.Operation {
	now := s.now().UTC()
	return domain.Operation{
		ID: domain.OperationID(s.newID()), OwnerID: owner, Type: kind, TargetType: targetType, TargetID: targetID,
		Status: domain.OperationPending, Stage: "runtime", IdempotencyKey: key, RequestHash: hash,
		CreatedAt: now, UpdatedAt: now,
	}
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

func isNotFound(err error) bool {
	var domainErr *domain.Error
	return errors.As(err, &domainErr) && domainErr.Code == domain.CodeNotFound
}
