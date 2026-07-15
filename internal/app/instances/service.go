// SPDX-License-Identifier: AGPL-3.0-only

// Package instances orchestrates persistent OpenBox instances through a runtime boundary.
package instances

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

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/images"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

const (
	MetadataManaged    = "user.openbox.managed"
	MetadataResource   = "user.openbox.resource"
	MetadataInstanceID = "user.openbox.instance_id"
	MetadataOwnerID    = "user.openbox.owner_id"
)

type ContainerRuntime interface {
	DiscoverCapabilities(context.Context) (runtimeapi.Capabilities, error)
	ListImages(context.Context) ([]runtimeapi.Image, error)
	InspectInstance(context.Context, string) (runtimeapi.Instance, error)
	CreateInstance(context.Context, runtimeapi.CreateRequest) (runtimeapi.Instance, error)
	StartInstance(context.Context, string) error
	StopInstance(context.Context, string) error
	DeleteInstance(context.Context, string) error
}

type Repository interface {
	EnsureImage(context.Context, domain.Image) error
	GetOperationByIdempotency(context.Context, domain.OwnerID, string) (domain.Operation, bool, error)
	CreateInstance(context.Context, domain.Instance, domain.Operation) (domain.Operation, bool, error)
	GetInstance(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error)
	UpdateInstanceState(context.Context, domain.OwnerID, domain.InstanceID, domain.DesiredState, domain.ObservedState, time.Time, domain.Operation) error
	UpdateInstanceObservation(context.Context, domain.OwnerID, domain.InstanceID, string, domain.IsolationType, domain.ObservedState, domain.ErrorCode, time.Time) error
	IsInstanceTombstoned(context.Context, domain.OwnerID, domain.InstanceID) (bool, error)
	FinalizeInstanceDeletion(context.Context, domain.OwnerID, domain.InstanceID, domain.OperationID, time.Time) error
	CompleteOperation(context.Context, domain.OwnerID, domain.OperationID, time.Time) error
}

type CapabilityError struct {
	Capability string
	Reason     string
}

func (e *CapabilityError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("container capability %q is unavailable", e.Capability)
	}
	return fmt.Sprintf("container capability %q is unavailable: %s", e.Capability, e.Reason)
}

type IdentityConflictError struct {
	RuntimeRef string
	Expected   domain.InstanceID
	Actual     string
}

func (e *IdentityConflictError) Error() string {
	return fmt.Sprintf("runtime resource %q belongs to identity %q, expected %q", e.RuntimeRef, e.Actual, e.Expected)
}

type Options struct {
	Now   func() time.Time
	NewID func() string
}

type Service struct {
	runtime      ContainerRuntime
	repo         Repository
	now          func() time.Time
	newID        func() string
	mutationGate chan struct{}
}

func New(runtime ContainerRuntime, repo Repository, options Options) (*Service, error) {
	if runtime == nil || repo == nil {
		return nil, errors.New("runtime and repository are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = randomID
	}
	service := &Service{runtime: runtime, repo: repo, now: options.Now, newID: options.NewID, mutationGate: make(chan struct{}, 1)}
	service.mutationGate <- struct{}{}
	return service, nil
}

type CreateInput struct {
	OwnerID            domain.OwnerID
	Name               string
	Kind               domain.InstanceKind
	Image              string
	RequestedIsolation domain.IsolationRequest
	Resources          domain.Resources
	OwnerPublicKey     string
	IdempotencyKey     string
}

func (s *Service) Create(ctx context.Context, input CreateInput) (domain.Instance, error) {
	if input.OwnerID == "" || input.Image == "" || input.OwnerPublicKey == "" || input.IdempotencyKey == "" {
		return domain.Instance{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "create"}
	}
	if input.Kind == "" {
		input.Kind = domain.KindVPS
	}
	requestHash, err := hashCreateInput(input)
	if err != nil {
		return domain.Instance{}, err
	}
	previous, found, err := s.repo.GetOperationByIdempotency(ctx, input.OwnerID, input.IdempotencyKey)
	if err != nil {
		return domain.Instance{}, err
	}
	if found {
		if previous.Type != "instance.create" || previous.RequestHash != requestHash {
			return domain.Instance{}, &domain.Error{Code: domain.CodeIdempotencyConflict, Field: "idempotency_key"}
		}
		return s.repo.GetInstance(ctx, input.OwnerID, domain.InstanceID(previous.TargetID))
	}
	if input.RequestedIsolation == "" {
		input.RequestedIsolation = domain.IsolationBestAvailable
	}
	if input.RequestedIsolation == domain.IsolationStrong {
		return domain.Instance{}, &CapabilityError{Capability: "strong_isolation", Reason: "slice 03 supports unprivileged containers only"}
	}
	capabilities, err := s.runtime.DiscoverCapabilities(ctx)
	if err != nil {
		return domain.Instance{}, fmt.Errorf("discover container capabilities: %w", err)
	}
	if !capabilities.Containers {
		return domain.Instance{}, &CapabilityError{Capability: "containers"}
	}
	available, err := s.runtime.ListImages(ctx)
	if err != nil {
		return domain.Instance{}, fmt.Errorf("list runtime images: %w", err)
	}
	image, err := images.Resolve(input.Image, available)
	if err != nil {
		return domain.Instance{}, err
	}
	if image.Type == "virtual-machine" {
		return domain.Instance{}, &CapabilityError{Capability: "container_image", Reason: "the selected image is VM-only"}
	}
	if capabilities.Architecture != "" && image.Architecture != "" && capabilities.Architecture != image.Architecture {
		return domain.Instance{}, &CapabilityError{Capability: "image_architecture", Reason: image.Architecture + " image on " + capabilities.Architecture + " host"}
	}
	if input.OwnerPublicKey != "" && !image.CloudInit {
		return domain.Instance{}, &CapabilityError{Capability: "image_cloud_init", Reason: "the selected image does not advertise variant=cloud or the OpenBox cloud-init override"}
	}

	now := s.now().UTC()
	instanceID := domain.InstanceID(s.newID())
	runtimeRef := runtimeReference(instanceID)
	existingRuntime, runtimeExists, err := s.inspectOptional(ctx, runtimeRef)
	if err != nil {
		return domain.Instance{}, err
	}
	if runtimeExists {
		if err := verifyIdentity(existingRuntime, instanceID); err != nil {
			return domain.Instance{}, err
		}
	}
	imageRecord := domain.Image{
		ID: domain.ImageID(image.Fingerprint), OwnerID: input.OwnerID, Alias: image.Fingerprint,
		Source: "incus:" + input.Image, Digest: image.Fingerprint, Architecture: image.Architecture,
		Compatibility: "container", CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repo.EnsureImage(ctx, imageRecord); err != nil {
		return domain.Instance{}, err
	}
	instance, err := domain.NewInstance(instanceID, input.OwnerID, input.Name, input.Kind, now)
	if err != nil {
		return domain.Instance{}, err
	}
	instance.ImageID = imageRecord.ID
	instance.RequestedIsolation = input.RequestedIsolation
	instance.ActualIsolation = domain.IsolationContainer
	instance.Resources = input.Resources
	instance.RuntimeRef = runtimeRef
	if err := domain.ValidateInstance(instance); err != nil {
		return domain.Instance{}, err
	}
	operation := s.operation("instance.create", instance, input.IdempotencyKey, requestHash)
	original, replay, err := s.repo.CreateInstance(ctx, instance, operation)
	if err != nil {
		return domain.Instance{}, err
	}
	if replay {
		return s.repo.GetInstance(ctx, input.OwnerID, domain.InstanceID(original.TargetID))
	}
	if err := s.repo.UpdateInstanceObservation(ctx, input.OwnerID, instance.ID, runtimeRef, domain.IsolationContainer, domain.ObservedCreating, "", s.now()); err != nil {
		return domain.Instance{}, err
	}
	if !runtimeExists {
		created, createErr := s.runtime.CreateInstance(ctx, runtimeapi.CreateRequest{
			Ref: runtimeRef, Image: image.Fingerprint, Unprivileged: true, OwnerPublicKey: input.OwnerPublicKey,
			Resources: runtimeapi.Resources{VCPUs: input.Resources.VCPUs, MemoryBytes: input.Resources.MemoryBytes, DiskBytes: input.Resources.DiskBytes},
			Metadata:  managedMetadata(input.OwnerID, instance.ID),
		})
		if createErr != nil {
			s.markError(ctx, instance, createErr)
			return domain.Instance{}, fmt.Errorf("create container: %w", createErr)
		}
		if err := verifyContainer(created, instance.ID); err != nil {
			s.markError(ctx, instance, err)
			return domain.Instance{}, err
		}
	}
	if !runtimeExists || existingRuntime.State != runtimeapi.StateRunning {
		if err := s.runtime.StartInstance(ctx, runtimeRef); err != nil {
			s.markError(ctx, instance, err)
			return domain.Instance{}, fmt.Errorf("start container: %w", err)
		}
	}
	result, err := s.Refresh(ctx, input.OwnerID, instance.ID)
	if err != nil {
		return domain.Instance{}, err
	}
	if err := s.repo.CompleteOperation(ctx, input.OwnerID, operation.ID, s.now()); err != nil {
		return domain.Instance{}, err
	}
	return result, nil
}

func (s *Service) Inspect(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID) (domain.Instance, error) {
	return s.Refresh(ctx, ownerID, id)
}

func (s *Service) Refresh(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID) (domain.Instance, error) {
	instance, err := s.repo.GetInstance(ctx, ownerID, id)
	if err != nil {
		return domain.Instance{}, err
	}
	runtimeInstance, err := s.runtime.InspectInstance(ctx, instance.RuntimeRef)
	if errors.Is(err, runtimeapi.ErrNotFound) {
		return domain.Instance{}, s.recordRuntimeMissing(ctx, instance, err)
	}
	if err != nil {
		return domain.Instance{}, fmt.Errorf("inspect container: %w", err)
	}
	if err := verifyContainer(runtimeInstance, id); err != nil {
		return domain.Instance{}, err
	}
	target, err := observedState(runtimeInstance.State)
	if err != nil {
		return domain.Instance{}, err
	}
	if err := s.syncObserved(ctx, instance, target); err != nil {
		return domain.Instance{}, err
	}
	return s.repo.GetInstance(ctx, ownerID, id)
}

func (s *Service) Start(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, key string) (domain.Instance, error) {
	release, err := s.acquireMutation(ctx)
	if err != nil {
		return domain.Instance{}, err
	}
	defer release()
	instance, runtimeInstance, err := s.loadVerified(ctx, ownerID, id)
	if err != nil {
		return domain.Instance{}, err
	}
	op, replay, err := s.mutationOperation(ctx, "instance.start", instance, key, "start:"+string(id))
	if err != nil {
		return domain.Instance{}, err
	}
	if replay && op.Status == domain.OperationSucceeded {
		return instance, nil
	}
	if replay && instance.DesiredState == domain.DesiredRunning && instance.ObservedState == domain.ObservedRunning && runtimeInstance.State == runtimeapi.StateRunning {
		if err := s.repo.CompleteOperation(ctx, ownerID, op.ID, s.now()); err != nil {
			return domain.Instance{}, err
		}
		return instance, nil
	}
	if !replay {
		if err := s.repo.UpdateInstanceState(ctx, ownerID, id, domain.DesiredRunning, instance.ObservedState, s.now(), op); err != nil {
			return domain.Instance{}, err
		}
	}
	if runtimeInstance.State != runtimeapi.StateRunning {
		if err := s.runtime.StartInstance(ctx, instance.RuntimeRef); err != nil {
			return domain.Instance{}, fmt.Errorf("start container: %w", err)
		}
	}
	result, err := s.Refresh(ctx, ownerID, id)
	if err != nil {
		return domain.Instance{}, err
	}
	if err := s.repo.CompleteOperation(ctx, ownerID, op.ID, s.now()); err != nil {
		return domain.Instance{}, err
	}
	return result, nil
}

func (s *Service) Stop(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, key string) (domain.Instance, error) {
	release, err := s.acquireMutation(ctx)
	if err != nil {
		return domain.Instance{}, err
	}
	defer release()
	instance, runtimeInstance, err := s.loadVerified(ctx, ownerID, id)
	if err != nil {
		return domain.Instance{}, err
	}
	target := instance.ObservedState
	if runtimeInstance.State == runtimeapi.StateRunning {
		target = domain.ObservedStopping
	}
	op, replay, err := s.mutationOperation(ctx, "instance.stop", instance, key, "stop:"+string(id))
	if err != nil {
		return domain.Instance{}, err
	}
	if replay && op.Status == domain.OperationSucceeded {
		return instance, nil
	}
	if replay && instance.DesiredState == domain.DesiredStopped && instance.ObservedState == domain.ObservedStopped && runtimeInstance.State == runtimeapi.StateStopped {
		if err := s.repo.CompleteOperation(ctx, ownerID, op.ID, s.now()); err != nil {
			return domain.Instance{}, err
		}
		return instance, nil
	}
	if !replay {
		if err := s.repo.UpdateInstanceState(ctx, ownerID, id, domain.DesiredStopped, target, s.now(), op); err != nil {
			return domain.Instance{}, err
		}
	}
	if runtimeInstance.State == runtimeapi.StateRunning {
		if err := s.runtime.StopInstance(ctx, instance.RuntimeRef); err != nil {
			return domain.Instance{}, fmt.Errorf("stop container: %w", err)
		}
	}
	result, err := s.Refresh(ctx, ownerID, id)
	if err != nil {
		return domain.Instance{}, err
	}
	if err := s.repo.CompleteOperation(ctx, ownerID, op.ID, s.now()); err != nil {
		return domain.Instance{}, err
	}
	return result, nil
}

func (s *Service) Restart(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, key string) (domain.Instance, error) {
	release, err := s.acquireMutation(ctx)
	if err != nil {
		return domain.Instance{}, err
	}
	defer release()
	instance, runtimeInstance, err := s.loadVerified(ctx, ownerID, id)
	if err != nil {
		return domain.Instance{}, err
	}
	target := instance.ObservedState
	if runtimeInstance.State == runtimeapi.StateRunning {
		target = domain.ObservedStopping
	}
	op, replay, err := s.mutationOperation(ctx, "instance.restart", instance, key, "restart:"+string(id))
	if err != nil {
		return domain.Instance{}, err
	}
	if replay && op.Status == domain.OperationSucceeded {
		return instance, nil
	}
	if replay && instance.DesiredState == domain.DesiredRunning && instance.ObservedState == domain.ObservedRunning && runtimeInstance.State == runtimeapi.StateRunning {
		if err := s.repo.CompleteOperation(ctx, ownerID, op.ID, s.now()); err != nil {
			return domain.Instance{}, err
		}
		return instance, nil
	}
	if !replay {
		if err := s.repo.UpdateInstanceState(ctx, ownerID, id, domain.DesiredRunning, target, s.now(), op); err != nil {
			return domain.Instance{}, err
		}
	}
	if runtimeInstance.State == runtimeapi.StateRunning {
		if err := s.runtime.StopInstance(ctx, instance.RuntimeRef); err != nil {
			return domain.Instance{}, fmt.Errorf("restart stop container: %w", err)
		}
		if err := s.syncObserved(ctx, instance, domain.ObservedStopped); err != nil {
			return domain.Instance{}, err
		}
		runtimeInstance.State = runtimeapi.StateStopped
	}
	if runtimeInstance.State != runtimeapi.StateRunning {
		if err := s.runtime.StartInstance(ctx, instance.RuntimeRef); err != nil {
			return domain.Instance{}, fmt.Errorf("restart start container: %w", err)
		}
	}
	result, err := s.Refresh(ctx, ownerID, id)
	if err != nil {
		return domain.Instance{}, err
	}
	if err := s.repo.CompleteOperation(ctx, ownerID, op.ID, s.now()); err != nil {
		return domain.Instance{}, err
	}
	return result, nil
}

func (s *Service) Delete(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, key string) error {
	release, err := s.acquireMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	instance, err := s.repo.GetInstance(ctx, ownerID, id)
	if err != nil {
		var domainErr *domain.Error
		if errors.As(err, &domainErr) && domainErr.Code == domain.CodeNotFound {
			deleted, lookupErr := s.repo.IsInstanceTombstoned(ctx, ownerID, id)
			if lookupErr != nil {
				return lookupErr
			}
			if deleted {
				return nil
			}
		}
		return err
	}
	runtimeInstance, exists, err := s.inspectOptional(ctx, instance.RuntimeRef)
	if err != nil {
		return err
	}
	if exists {
		if err := verifyContainer(runtimeInstance, id); err != nil {
			return err
		}
	}
	op, replay, err := s.mutationOperation(ctx, "instance.delete", instance, key, "delete:"+string(id))
	if err != nil {
		return err
	}
	if !replay {
		if err := s.repo.UpdateInstanceState(ctx, ownerID, id, domain.DesiredDeleted, domain.ObservedDeleting, s.now(), op); err != nil {
			return err
		}
	}
	if exists && runtimeInstance.State == runtimeapi.StateRunning {
		if err := s.runtime.StopInstance(ctx, instance.RuntimeRef); err != nil {
			return fmt.Errorf("stop container for deletion: %w", err)
		}
	}
	if exists {
		if err := s.runtime.DeleteInstance(ctx, instance.RuntimeRef); err != nil && !errors.Is(err, runtimeapi.ErrNotFound) {
			return fmt.Errorf("delete container: %w", err)
		}
	}
	remaining, stillExists, err := s.inspectOptional(ctx, instance.RuntimeRef)
	if err != nil {
		return err
	}
	if stillExists {
		if identityErr := verifyIdentity(remaining, id); identityErr != nil {
			return identityErr
		}
		return fmt.Errorf("runtime resource %q still exists after deletion", instance.RuntimeRef)
	}
	if err := s.repo.UpdateInstanceObservation(ctx, ownerID, id, instance.RuntimeRef, domain.IsolationContainer, domain.ObservedDeleted, "", s.now()); err != nil {
		return err
	}
	return s.repo.FinalizeInstanceDeletion(ctx, ownerID, id, op.ID, s.now())
}

func (s *Service) loadVerified(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID) (domain.Instance, runtimeapi.Instance, error) {
	instance, err := s.repo.GetInstance(ctx, ownerID, id)
	if err != nil {
		return domain.Instance{}, runtimeapi.Instance{}, err
	}
	runtimeInstance, err := s.runtime.InspectInstance(ctx, instance.RuntimeRef)
	if errors.Is(err, runtimeapi.ErrNotFound) {
		return domain.Instance{}, runtimeapi.Instance{}, s.recordRuntimeMissing(ctx, instance, err)
	}
	if err != nil {
		return domain.Instance{}, runtimeapi.Instance{}, err
	}
	if err := verifyContainer(runtimeInstance, id); err != nil {
		return domain.Instance{}, runtimeapi.Instance{}, err
	}
	return instance, runtimeInstance, nil
}

func (s *Service) recordRuntimeMissing(ctx context.Context, instance domain.Instance, cause error) error {
	if err := s.repo.UpdateInstanceObservation(ctx, instance.OwnerID, instance.ID, instance.RuntimeRef, domain.IsolationContainer, domain.ObservedError, domain.CodeRuntimeMissing, s.now()); err != nil {
		return fmt.Errorf("record missing runtime: %w", err)
	}
	return &domain.Error{Code: domain.CodeRuntimeMissing, Field: "runtime_ref", Cause: cause}
}

func (s *Service) syncObserved(ctx context.Context, instance domain.Instance, target domain.ObservedState) error {
	current := instance.ObservedState
	for current != target {
		next, ok := nextObserved(current, target)
		if !ok {
			return &domain.Error{Code: domain.CodeInvalidTransition, Field: "observed_state"}
		}
		if err := s.repo.UpdateInstanceObservation(ctx, instance.OwnerID, instance.ID, instance.RuntimeRef, domain.IsolationContainer, next, "", s.now()); err != nil {
			return err
		}
		current = next
	}
	return nil
}

func nextObserved(current, target domain.ObservedState) (domain.ObservedState, bool) {
	if current == target {
		return target, true
	}
	if target == domain.ObservedRunning {
		switch current {
		case domain.ObservedPending:
			return domain.ObservedCreating, true
		case domain.ObservedCreating, domain.ObservedStopped, domain.ObservedError:
			return domain.ObservedRunning, true
		case domain.ObservedStopping:
			return domain.ObservedStopped, true
		}
	}
	if target == domain.ObservedStopped {
		switch current {
		case domain.ObservedRunning:
			return domain.ObservedStopping, true
		case domain.ObservedStopping:
			return domain.ObservedStopped, true
		case domain.ObservedError:
			return domain.ObservedStopped, true
		}
	}
	return "", false
}

func (s *Service) inspectOptional(ctx context.Context, ref string) (runtimeapi.Instance, bool, error) {
	instance, err := s.runtime.InspectInstance(ctx, ref)
	if errors.Is(err, runtimeapi.ErrNotFound) {
		return runtimeapi.Instance{}, false, nil
	}
	if err != nil {
		return runtimeapi.Instance{}, false, fmt.Errorf("inspect runtime identity: %w", err)
	}
	return instance, true, nil
}

func (s *Service) operation(kind string, instance domain.Instance, key, hash string) domain.Operation {
	now := s.now().UTC()
	return domain.Operation{ID: domain.OperationID(s.newID()), OwnerID: instance.OwnerID, Type: kind, TargetType: "instance", TargetID: string(instance.ID), Status: domain.OperationPending, Stage: "runtime", IdempotencyKey: key, RequestHash: hash, CreatedAt: now, UpdatedAt: now}
}

func (s *Service) acquireMutation(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.mutationGate:
		return func() { s.mutationGate <- struct{}{} }, nil
	}
}

func (s *Service) mutationOperation(ctx context.Context, kind string, instance domain.Instance, key, hash string) (domain.Operation, bool, error) {
	if key == "" {
		return domain.Operation{}, false, &domain.Error{Code: domain.CodeInvalidArgument, Field: "idempotency_key"}
	}
	existing, found, err := s.repo.GetOperationByIdempotency(ctx, instance.OwnerID, key)
	if err != nil {
		return domain.Operation{}, false, err
	}
	if found {
		if existing.Type != kind || existing.TargetType != "instance" || existing.TargetID != string(instance.ID) || existing.RequestHash != hash {
			return domain.Operation{}, false, &domain.Error{Code: domain.CodeIdempotencyConflict, Field: "idempotency_key"}
		}
		return existing, true, nil
	}
	operation := s.operation(kind, instance, key, hash)
	operation.Status = domain.OperationRunning
	return operation, false, nil
}

func (s *Service) markError(ctx context.Context, instance domain.Instance, cause error) {
	_ = s.repo.UpdateInstanceObservation(ctx, instance.OwnerID, instance.ID, instance.RuntimeRef, domain.IsolationContainer, domain.ObservedError, domain.ErrorCode("runtime_error"), s.now())
}

func managedMetadata(ownerID domain.OwnerID, instanceID domain.InstanceID) map[string]string {
	return map[string]string{MetadataManaged: "true", MetadataResource: "instance", MetadataInstanceID: string(instanceID), MetadataOwnerID: string(ownerID)}
}

func verifyIdentity(instance runtimeapi.Instance, expected domain.InstanceID) error {
	actual := instance.Metadata[MetadataInstanceID]
	if instance.Metadata[MetadataManaged] != "true" || actual != string(expected) {
		return &IdentityConflictError{RuntimeRef: instance.Ref, Expected: expected, Actual: actual}
	}
	return nil
}

func verifyContainer(instance runtimeapi.Instance, expected domain.InstanceID) error {
	if err := verifyIdentity(instance, expected); err != nil {
		return err
	}
	if instance.IsVM {
		return &CapabilityError{Capability: "container", Reason: "runtime returned a virtual machine"}
	}
	if instance.Privileged {
		return &CapabilityError{Capability: "unprivileged_container", Reason: "runtime returned a privileged container"}
	}
	return nil
}

func observedState(state runtimeapi.InstanceState) (domain.ObservedState, error) {
	switch state {
	case runtimeapi.StateRunning:
		return domain.ObservedRunning, nil
	case runtimeapi.StateStopped:
		return domain.ObservedStopped, nil
	default:
		return "", &CapabilityError{Capability: "instance_state", Reason: string(state)}
	}
}

func runtimeReference(id domain.InstanceID) string {
	sum := sha256.Sum256([]byte(id))
	return "obx-" + hex.EncodeToString(sum[:12])
}

func hashCreateInput(input CreateInput) (string, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("hash create request: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func randomID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return strings.ToLower(hex.EncodeToString(value))
}
