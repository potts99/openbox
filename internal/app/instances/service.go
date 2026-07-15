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
	"github.com/openbox-dev/openbox/internal/operations"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/sandbox"
)

const (
	MetadataManaged    = "user.openbox.managed"
	MetadataResource   = "user.openbox.resource"
	MetadataInstanceID = "user.openbox.instance_id"
	MetadataOwnerID    = "user.openbox.owner_id"
)

type InstanceRuntime interface {
	DiscoverCapabilities(context.Context) (runtimeapi.Capabilities, error)
	ListImages(context.Context) ([]runtimeapi.Image, error)
	InspectInstance(context.Context, string) (runtimeapi.Instance, error)
	CreateInstance(context.Context, runtimeapi.CreateRequest) (runtimeapi.Instance, error)
	StartInstance(context.Context, string) error
	WaitInstanceReady(context.Context, runtimeapi.ReadinessRequest) error
	StopInstance(context.Context, string) error
	Exec(context.Context, runtimeapi.ExecRequest) (runtimeapi.ExecResult, error)
	DeleteInstance(context.Context, string) error
}

// ContainerRuntime is retained as a source-compatible name for slice 03 callers.
type ContainerRuntime = InstanceRuntime

type Repository interface {
	EnsureImage(context.Context, domain.Image) error
	GetOperationByIdempotency(context.Context, domain.OwnerID, string) (domain.Operation, bool, error)
	CreateInstance(context.Context, domain.Instance, domain.Operation) (domain.Operation, bool, error)
	GetInstance(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error)
	UpdateInstanceState(context.Context, domain.OwnerID, domain.InstanceID, domain.DesiredState, domain.ObservedState, time.Time, domain.Operation) error
	UpdateInstanceProtection(context.Context, domain.OwnerID, domain.InstanceID, bool, time.Time) error
	UpdateInstanceExpiry(context.Context, domain.OwnerID, domain.InstanceID, time.Time, time.Time) error
	UpdateInstanceObservation(context.Context, domain.OwnerID, domain.InstanceID, string, domain.IsolationType, domain.ObservedState, domain.ErrorCode, time.Time) error
	IsInstanceTombstoned(context.Context, domain.OwnerID, domain.InstanceID) (bool, error)
	FinalizeInstanceDeletion(context.Context, domain.OwnerID, domain.InstanceID, domain.OperationID, time.Time) error
	CompleteOperation(context.Context, domain.OwnerID, domain.OperationID, time.Time) error
	UpdateOperationStage(context.Context, domain.OwnerID, domain.OperationID, string, int, time.Time) error
	ListInstancesByOwner(context.Context, domain.OwnerID, int) ([]domain.Instance, error)
	ListImagesByOwner(context.Context, domain.OwnerID, int) ([]domain.Image, error)
	GetOperation(context.Context, domain.OwnerID, domain.OperationID) (domain.Operation, error)
	ListOperations(context.Context, domain.OwnerID, int) ([]domain.Operation, error)
	ListOperationEventsAfter(context.Context, domain.OwnerID, domain.OperationID, int, int) ([]domain.OperationEvent, error)
	CancelPendingOperation(context.Context, domain.OwnerID, domain.OperationID, time.Time) (domain.Operation, error)
}

type CapabilityError struct {
	Capability string
	Reason     string
}

func (e *CapabilityError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("runtime capability %q is unavailable", e.Capability)
	}
	return fmt.Sprintf("runtime capability %q is unavailable: %s", e.Capability, e.Reason)
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
	Now                      func() time.Time
	NewID                    func() string
	Mode                     *operations.Mode
	InstanceGatewayPublicKey string
}

type Service struct {
	runtime                  InstanceRuntime
	repo                     Repository
	now                      func() time.Time
	newID                    func() string
	mode                     *operations.Mode
	instanceGatewayPublicKey string
	mutationGate             chan struct{}
}

func New(runtime InstanceRuntime, repo Repository, options Options) (*Service, error) {
	if runtime == nil || repo == nil {
		return nil, errors.New("runtime and repository are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = randomID
	}
	if options.Mode == nil {
		options.Mode = &operations.Mode{}
	}
	service := &Service{runtime: runtime, repo: repo, now: options.Now, newID: options.NewID, mode: options.Mode, instanceGatewayPublicKey: strings.TrimSpace(options.InstanceGatewayPublicKey), mutationGate: make(chan struct{}, 1)}
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
	Lifetime           time.Duration // Sandbox only; 0 means kind default
	OwnerPublicKey     string
	IdempotencyKey     string
}

type createRecoveryPayload struct {
	OwnerPublicKey string `json:"owner_public_key"`
}

type MutationAction string

const (
	MutationStart   MutationAction = "start"
	MutationStop    MutationAction = "stop"
	MutationRestart MutationAction = "restart"
	MutationDelete  MutationAction = "delete"
)

type mutationRecoveryPayload struct {
	PreviousDesired  domain.DesiredState  `json:"previous_desired_state"`
	PreviousObserved domain.ObservedState `json:"previous_observed_state"`
	IntendedDesired  domain.DesiredState  `json:"intended_desired_state"`
	IntendedObserved domain.ObservedState `json:"intended_observed_state"`
}

// SubmitCreate validates and durably records a create without performing a
// runtime mutation. Durable workers execute the returned operation separately.
func (s *Service) SubmitCreate(ctx context.Context, input CreateInput) (domain.Instance, domain.Operation, error) {
	if input.OwnerID == "" || input.OwnerPublicKey == "" || input.IdempotencyKey == "" {
		return domain.Instance{}, domain.Operation{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "create"}
	}
	release, err := s.acquireMutation(ctx)
	if err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	defer release()
	lifetime, err := applyCreateDefaults(&input)
	if err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	requestHash, err := hashCreateInput(input)
	if err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	previous, found, err := s.repo.GetOperationByIdempotency(ctx, input.OwnerID, input.IdempotencyKey)
	if err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	if found {
		if previous.Type != "instance.create" || previous.RequestHash != requestHash {
			return domain.Instance{}, domain.Operation{}, &domain.Error{Code: domain.CodeIdempotencyConflict, Field: "idempotency_key"}
		}
		instance, getErr := s.repo.GetInstance(ctx, input.OwnerID, domain.InstanceID(previous.TargetID))
		if getErr != nil && previous.ErrorCode == domain.CodeOperationCanceled {
			return domain.Instance{}, previous, nil
		}
		return instance, previous, getErr
	}
	if s.mode.Degraded() {
		return domain.Instance{}, domain.Operation{}, &domain.Error{Code: domain.CodeUnavailable, Field: "runtime"}
	}
	capabilities, err := s.runtime.DiscoverCapabilities(ctx)
	if err != nil {
		return domain.Instance{}, domain.Operation{}, fmt.Errorf("discover runtime capabilities: %w", err)
	}
	actualIsolation, err := selectIsolation(input.RequestedIsolation, capabilities)
	if err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	available, err := s.runtime.ListImages(ctx)
	if err != nil {
		return domain.Instance{}, domain.Operation{}, fmt.Errorf("list runtime images: %w", err)
	}
	imageType := "container"
	if actualIsolation == domain.IsolationVM {
		imageType = "virtual-machine"
	}
	image, err := images.ResolveForType(input.Image, imageType, available)
	if err != nil {
		if _, anyTypeErr := images.Resolve(input.Image, available); anyTypeErr == nil {
			return domain.Instance{}, domain.Operation{}, &CapabilityError{Capability: imageType + "_image", Reason: "the selected image is incompatible with " + imageType + " isolation"}
		}
		return domain.Instance{}, domain.Operation{}, err
	}
	if capabilities.Architecture != "" && image.Architecture != "" && capabilities.Architecture != image.Architecture {
		return domain.Instance{}, domain.Operation{}, &CapabilityError{Capability: "image_architecture", Reason: image.Architecture + " image on " + capabilities.Architecture + " host"}
	}
	if !image.CloudInit {
		return domain.Instance{}, domain.Operation{}, &CapabilityError{Capability: "image_cloud_init", Reason: "the selected image does not advertise variant=cloud or the OpenBox cloud-init override"}
	}
	now := s.now().UTC()
	instanceID := domain.InstanceID(s.newID())
	imageRecord := domain.Image{ID: domain.ImageID(image.Fingerprint), OwnerID: input.OwnerID, Alias: image.Fingerprint, Source: "incus:" + input.Image, Digest: image.Fingerprint, Architecture: image.Architecture, Compatibility: imageType, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.EnsureImage(ctx, imageRecord); err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	instance, err := domain.NewInstance(instanceID, input.OwnerID, input.Name, input.Kind, now)
	if err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	instance.ImageID = imageRecord.ID
	instance.RequestedIsolation = input.RequestedIsolation
	instance.ActualIsolation = actualIsolation
	instance.Resources = input.Resources
	instance.RuntimeRef = runtimeReference(instanceID)
	applyLifetime(&instance, lifetime, now)
	if err := domain.ValidateInstance(instance); err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	operation := s.operation("instance.create", instance, input.IdempotencyKey, requestHash)
	operation.PayloadJSON, err = json.Marshal(createRecoveryPayload{OwnerPublicKey: input.OwnerPublicKey})
	if err != nil {
		return domain.Instance{}, domain.Operation{}, fmt.Errorf("encode create recovery payload: %w", err)
	}
	original, replay, err := s.repo.CreateInstance(ctx, instance, operation)
	if err != nil {
		return domain.Instance{}, domain.Operation{}, err
	}
	if replay {
		instance, err = s.repo.GetInstance(ctx, input.OwnerID, domain.InstanceID(original.TargetID))
		return instance, original, err
	}
	return instance, operation, nil
}

// SubmitAction records desired state and a pending operation atomically. It
// deliberately performs no runtime inspection or mutation.
func (s *Service) SubmitAction(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, action MutationAction, key string) (domain.Operation, error) {
	if key == "" {
		return domain.Operation{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "idempotency_key"}
	}
	release, err := s.acquireMutation(ctx)
	if err != nil {
		return domain.Operation{}, err
	}
	defer release()
	kind := "instance." + string(action)
	switch action {
	case MutationStart, MutationRestart, MutationStop, MutationDelete:
	default:
		return domain.Operation{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "action"}
	}
	hash := string(action) + ":" + string(id)
	existing, found, err := s.repo.GetOperationByIdempotency(ctx, ownerID, key)
	if err != nil {
		return domain.Operation{}, err
	}
	if found {
		if existing.Type != kind || existing.TargetID != string(id) || existing.RequestHash != hash {
			return domain.Operation{}, &domain.Error{Code: domain.CodeIdempotencyConflict, Field: "idempotency_key"}
		}
		return existing, nil
	}
	if s.mode.Degraded() {
		return domain.Operation{}, &domain.Error{Code: domain.CodeUnavailable, Field: "runtime"}
	}
	instance, err := s.repo.GetInstance(ctx, ownerID, id)
	if err != nil {
		return domain.Operation{}, err
	}
	if action == MutationDelete && instance.Protected {
		return domain.Operation{}, &domain.Error{Code: domain.CodeProtectedBase, Field: "desired_state"}
	}
	desired := instance.DesiredState
	observed := instance.ObservedState
	switch action {
	case MutationStart, MutationRestart:
		desired = domain.DesiredRunning
	case MutationStop:
		desired = domain.DesiredStopped
	case MutationDelete:
		desired = domain.DesiredDeleted
		observed = domain.ObservedDeleting
	}
	operation := s.operation(kind, instance, key, hash)
	operation.PayloadJSON, err = json.Marshal(mutationRecoveryPayload{
		PreviousDesired: instance.DesiredState, PreviousObserved: instance.ObservedState,
		IntendedDesired: desired, IntendedObserved: observed,
	})
	if err != nil {
		return domain.Operation{}, err
	}
	if err := s.repo.UpdateInstanceState(ctx, ownerID, id, desired, observed, s.now(), operation); err != nil {
		return domain.Operation{}, err
	}
	return operation, nil
}

// SubmitMutation is retained as a descriptive alias for callers outside the
// HTTP transport.
func (s *Service) SubmitMutation(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, action MutationAction, key string) (domain.Operation, error) {
	return s.SubmitAction(ctx, ownerID, id, action, key)
}

func (s *Service) CancelOperation(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID) (domain.Operation, error) {
	return s.repo.CancelPendingOperation(ctx, ownerID, id, s.now().UTC())
}

// SetProtection marks or clears Devbox base protection. Protected bases cannot
// be deleted until protection is explicitly removed.
func (s *Service) SetProtection(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, protected bool) (domain.Instance, error) {
	release, err := s.acquireMutation(ctx)
	if err != nil {
		return domain.Instance{}, err
	}
	defer release()
	instance, err := s.repo.GetInstance(ctx, ownerID, id)
	if err != nil {
		return domain.Instance{}, err
	}
	if protected && instance.Kind != domain.KindDevbox {
		return domain.Instance{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "protected"}
	}
	if instance.Protected == protected {
		return instance, nil
	}
	if err := s.repo.UpdateInstanceProtection(ctx, ownerID, id, protected, s.now().UTC()); err != nil {
		return domain.Instance{}, err
	}
	return s.repo.GetInstance(ctx, ownerID, id)
}

// MarkExpired records desired deleted for an expired Sandbox without removing
// the runtime yet. Cleanup retries through Delete/reconcile until Incus
// confirms the resource is gone; observed state stays deleting until then.
func (s *Service) MarkExpired(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID) error {
	instance, err := s.repo.GetInstance(ctx, ownerID, id)
	if err != nil {
		return err
	}
	if instance.Kind != domain.KindSandbox {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "kind"}
	}
	if instance.DesiredState == domain.DesiredDeleted {
		return nil
	}
	if instance.ExpiresAt == nil {
		return &domain.Error{Code: domain.CodeExpiryRequired, Field: "expires_at"}
	}
	key := "expiry:" + string(id) + ":" + instance.ExpiresAt.UTC().Format(time.RFC3339Nano)
	_, err = s.SubmitAction(ctx, ownerID, id, MutationDelete, key)
	return err
}

// ExtendExpiry atomically lengthens a Sandbox TTL and rejects extension once
// irreversible deletion has begun.
func (s *Service) ExtendExpiry(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, by time.Duration) (domain.Instance, error) {
	release, err := s.acquireMutation(ctx)
	if err != nil {
		return domain.Instance{}, err
	}
	defer release()
	instance, err := s.repo.GetInstance(ctx, ownerID, id)
	if err != nil {
		return domain.Instance{}, err
	}
	now := s.now().UTC()
	extended, err := domain.ExtendSandboxExpiry(instance, by, now)
	if err != nil {
		return domain.Instance{}, err
	}
	if err := s.repo.UpdateInstanceExpiry(ctx, ownerID, id, *extended.ExpiresAt, extended.UpdatedAt); err != nil {
		return domain.Instance{}, err
	}
	return s.repo.GetInstance(ctx, ownerID, id)
}

// Exec runs an argv command inside a managed instance and streams framed
// output through sink. Output is never persisted to SQLite.
func (s *Service) Exec(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID, req sandbox.ExecRequest, sink sandbox.FrameSink) error {
	if sink == nil {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "exec"}
	}
	instance, err := s.repo.GetInstance(ctx, ownerID, id)
	if err != nil {
		return err
	}
	if instance.DesiredState == domain.DesiredDeleted || instance.ObservedState == domain.ObservedDeleting || instance.ObservedState == domain.ObservedDeleted {
		return &domain.Error{Code: domain.CodeInvalidTransition, Field: "instance"}
	}
	if instance.ObservedState != domain.ObservedRunning {
		return &domain.Error{Code: domain.CodeInvalidTransition, Field: "observed_state"}
	}
	return sandbox.Run(ctx, s.runtime, instance.RuntimeRef, req, sink)
}

func (s *Service) Capabilities(ctx context.Context) (runtimeapi.Capabilities, error) {
	return s.runtime.DiscoverCapabilities(ctx)
}

func (s *Service) Health(ctx context.Context) error {
	_, err := s.runtime.DiscoverCapabilities(ctx)
	return err
}

func (s *Service) ListImages(ctx context.Context, ownerID domain.OwnerID) ([]domain.Image, error) {
	return s.repo.ListImagesByOwner(ctx, ownerID, 100)
}

func (s *Service) ListInstances(ctx context.Context, ownerID domain.OwnerID) ([]domain.Instance, error) {
	return s.repo.ListInstancesByOwner(ctx, ownerID, 100)
}

func (s *Service) GetInstance(ctx context.Context, ownerID domain.OwnerID, id domain.InstanceID) (domain.Instance, error) {
	return s.repo.GetInstance(ctx, ownerID, id)
}

func (s *Service) GetOperation(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID) (domain.Operation, error) {
	return s.repo.GetOperation(ctx, ownerID, id)
}

func (s *Service) ListOperations(ctx context.Context, ownerID domain.OwnerID) ([]domain.Operation, error) {
	return s.repo.ListOperations(ctx, ownerID, 100)
}

func (s *Service) ListOperationEventsAfter(ctx context.Context, ownerID domain.OwnerID, id domain.OperationID, after, limit int) ([]domain.OperationEvent, error) {
	return s.repo.ListOperationEventsAfter(ctx, ownerID, id, after, limit)
}

func (s *Service) Create(ctx context.Context, input CreateInput) (domain.Instance, error) {
	if input.OwnerID == "" || input.OwnerPublicKey == "" || input.IdempotencyKey == "" {
		return domain.Instance{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "create"}
	}
	release, err := s.acquireMutation(ctx)
	if err != nil {
		return domain.Instance{}, err
	}
	defer release()
	lifetime, err := applyCreateDefaults(&input)
	if err != nil {
		return domain.Instance{}, err
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
	capabilities, err := s.runtime.DiscoverCapabilities(ctx)
	if err != nil {
		return domain.Instance{}, fmt.Errorf("discover runtime capabilities: %w", err)
	}
	actualIsolation, err := selectIsolation(input.RequestedIsolation, capabilities)
	if err != nil {
		return domain.Instance{}, err
	}
	available, err := s.runtime.ListImages(ctx)
	if err != nil {
		return domain.Instance{}, fmt.Errorf("list runtime images: %w", err)
	}
	imageType := "container"
	if actualIsolation == domain.IsolationVM {
		imageType = "virtual-machine"
	}
	image, err := images.ResolveForType(input.Image, imageType, available)
	if err != nil {
		if _, anyTypeErr := images.Resolve(input.Image, available); anyTypeErr == nil {
			return domain.Instance{}, &CapabilityError{Capability: imageType + "_image", Reason: "the selected image is incompatible with " + imageType + " isolation"}
		}
		return domain.Instance{}, err
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
		if err := verifyRuntime(existingRuntime, instanceID, actualIsolation); err != nil {
			return domain.Instance{}, err
		}
	}
	imageRecord := domain.Image{
		ID: domain.ImageID(image.Fingerprint), OwnerID: input.OwnerID, Alias: image.Fingerprint,
		Source: "incus:" + input.Image, Digest: image.Fingerprint, Architecture: image.Architecture,
		Compatibility: imageType, CreatedAt: now, UpdatedAt: now,
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
	instance.ActualIsolation = actualIsolation
	instance.Resources = input.Resources
	instance.RuntimeRef = runtimeRef
	applyLifetime(&instance, lifetime, now)
	if err := domain.ValidateInstance(instance); err != nil {
		return domain.Instance{}, err
	}
	operation := s.operation("instance.create", instance, input.IdempotencyKey, requestHash)
	payload, err := json.Marshal(createRecoveryPayload{OwnerPublicKey: input.OwnerPublicKey})
	if err != nil {
		return domain.Instance{}, fmt.Errorf("encode create recovery payload: %w", err)
	}
	operation.PayloadJSON = payload
	original, replay, err := s.repo.CreateInstance(ctx, instance, operation)
	if err != nil {
		return domain.Instance{}, err
	}
	if replay {
		return s.repo.GetInstance(ctx, input.OwnerID, domain.InstanceID(original.TargetID))
	}
	if err := s.repo.UpdateInstanceObservation(ctx, input.OwnerID, instance.ID, runtimeRef, actualIsolation, domain.ObservedCreating, "", s.now()); err != nil {
		return domain.Instance{}, err
	}
	createdByOperation := false
	stage := "creating_container"
	if actualIsolation == domain.IsolationVM {
		stage = "creating_vm"
	}
	if err := s.repo.UpdateOperationStage(ctx, input.OwnerID, operation.ID, stage, 25, s.now()); err != nil {
		return domain.Instance{}, err
	}
	if !runtimeExists {
		if actualIsolation == domain.IsolationVM {
			// A transport timeout can hide a successful Incus create. Treat every
			// VM create attempt as cleanup-eligible and discover the actual state.
			createdByOperation = true
		}
		created, createErr := s.runtime.CreateInstance(ctx, runtimeapi.CreateRequest{
			Ref: runtimeRef, Image: image.Fingerprint, VM: actualIsolation == domain.IsolationVM, Unprivileged: actualIsolation == domain.IsolationContainer, OwnerPublicKey: authorizedKeys(input.OwnerPublicKey, s.instanceGatewayPublicKey),
			Resources: runtimeapi.Resources{VCPUs: input.Resources.VCPUs, MemoryBytes: input.Resources.MemoryBytes, DiskBytes: input.Resources.DiskBytes},
			Metadata:  managedMetadata(input.OwnerID, instance.ID),
		})
		if createErr != nil {
			s.markError(ctx, instance, createErr)
			return domain.Instance{}, s.withPartialVMCleanup(instance, createdByOperation, fmt.Errorf("create %s: %w", imageType, createErr))
		}
		createdByOperation = true
		if err := verifyRuntime(created, instance.ID, actualIsolation); err != nil {
			s.markError(ctx, instance, err)
			return domain.Instance{}, s.withPartialVMCleanup(instance, createdByOperation, err)
		}
	}
	createdStage := "container_created"
	if actualIsolation == domain.IsolationVM {
		createdStage = "vm_created"
	}
	if err := s.repo.UpdateOperationStage(ctx, input.OwnerID, operation.ID, createdStage, 40, s.now()); err != nil {
		return domain.Instance{}, s.withPartialVMCleanup(instance, createdByOperation, err)
	}
	if !runtimeExists || existingRuntime.State != runtimeapi.StateRunning {
		startStage := "starting_container"
		if actualIsolation == domain.IsolationVM {
			startStage = "starting_vm"
		}
		if err := s.repo.UpdateOperationStage(ctx, input.OwnerID, operation.ID, startStage, 50, s.now()); err != nil {
			return domain.Instance{}, s.withPartialVMCleanup(instance, createdByOperation, err)
		}
		if err := s.runtime.StartInstance(ctx, runtimeRef); err != nil {
			s.markError(ctx, instance, err)
			return domain.Instance{}, s.withPartialVMCleanup(instance, createdByOperation, fmt.Errorf("start %s: %w", imageType, err))
		}
	}
	startedStage := "container_started"
	if actualIsolation == domain.IsolationVM {
		startedStage = "vm_started"
	}
	if err := s.repo.UpdateOperationStage(ctx, input.OwnerID, operation.ID, startedStage, 60, s.now()); err != nil {
		return domain.Instance{}, s.withPartialVMCleanup(instance, createdByOperation, err)
	}
	if actualIsolation == domain.IsolationVM {
		err := s.waitForVMReady(ctx, instance, operation)
		if err != nil {
			s.markError(ctx, instance, err)
			return domain.Instance{}, s.withPartialVMCleanup(instance, createdByOperation, fmt.Errorf("wait for VM readiness: %w", err))
		}
	}
	result, err := s.Refresh(ctx, input.OwnerID, instance.ID)
	if err != nil {
		return domain.Instance{}, s.withPartialVMCleanup(instance, createdByOperation, err)
	}
	if err := s.repo.CompleteOperation(ctx, input.OwnerID, operation.ID, s.now()); err != nil {
		return domain.Instance{}, err
	}
	return result, nil
}

// RecoverOperation resumes a durable lifecycle operation by inspecting the
// recorded runtime identity before taking another external action.
func (s *Service) RecoverOperation(ctx context.Context, operation domain.Operation) error {
	ownerID := operation.OwnerID
	id := domain.InstanceID(operation.TargetID)
	switch operation.Type {
	case "instance.create":
		return s.recoverCreate(ctx, operation)
	case "instance.start":
		_, err := s.Start(ctx, ownerID, id, operation.IdempotencyKey)
		return err
	case "instance.stop":
		_, err := s.Stop(ctx, ownerID, id, operation.IdempotencyKey)
		return err
	case "instance.restart":
		_, err := s.Restart(ctx, ownerID, id, operation.IdempotencyKey)
		return err
	case "instance.delete":
		return s.Delete(ctx, ownerID, id, operation.IdempotencyKey)
	default:
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.type"}
	}
}

func (s *Service) recoverCreate(ctx context.Context, operation domain.Operation) error {
	release, err := s.acquireMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	instance, err := s.repo.GetInstance(ctx, operation.OwnerID, domain.InstanceID(operation.TargetID))
	if err != nil {
		return err
	}
	var payload createRecoveryPayload
	if err := json.Unmarshal(operation.PayloadJSON, &payload); err != nil || payload.OwnerPublicKey == "" {
		return &domain.Error{Code: domain.CodeConflict, Field: "operation.payload", Cause: err}
	}
	runtimeInstance, exists, err := s.inspectOptional(ctx, instance.RuntimeRef)
	if err != nil {
		return err
	}
	if !exists {
		if operation.Stage != "runtime" && operation.Stage != "creating_container" && operation.Stage != "creating_vm" {
			return s.recordRuntimeMissing(ctx, instance, runtimeapi.ErrNotFound)
		}
		stage := "creating_container"
		if instance.ActualIsolation == domain.IsolationVM {
			stage = "creating_vm"
		}
		if err := s.repo.UpdateOperationStage(ctx, instance.OwnerID, operation.ID, stage, 25, s.now()); err != nil {
			return err
		}
		runtimeInstance, err = s.runtime.CreateInstance(ctx, runtimeapi.CreateRequest{
			Ref: instance.RuntimeRef, Image: string(instance.ImageID), VM: instance.ActualIsolation == domain.IsolationVM,
			Unprivileged: instance.ActualIsolation == domain.IsolationContainer, OwnerPublicKey: authorizedKeys(payload.OwnerPublicKey, s.instanceGatewayPublicKey),
			Resources: runtimeapi.Resources{VCPUs: instance.Resources.VCPUs, MemoryBytes: instance.Resources.MemoryBytes, DiskBytes: instance.Resources.DiskBytes},
			Metadata:  managedMetadata(instance.OwnerID, instance.ID),
		})
		if err != nil {
			if !errors.Is(err, runtimeapi.ErrAlreadyExists) {
				return fmt.Errorf("recover create runtime instance: %w", err)
			}
			runtimeInstance, err = s.runtime.InspectInstance(ctx, instance.RuntimeRef)
			if err != nil {
				return err
			}
		}
	}
	if err := verifyRuntime(runtimeInstance, instance.ID, instance.ActualIsolation); err != nil {
		return err
	}
	createdStage := "container_created"
	if instance.ActualIsolation == domain.IsolationVM {
		createdStage = "vm_created"
	}
	if err := s.repo.UpdateOperationStage(ctx, instance.OwnerID, operation.ID, createdStage, 40, s.now()); err != nil {
		return err
	}
	if runtimeInstance.State != runtimeapi.StateRunning {
		startStage := "starting_container"
		if instance.ActualIsolation == domain.IsolationVM {
			startStage = "starting_vm"
		}
		if err := s.repo.UpdateOperationStage(ctx, instance.OwnerID, operation.ID, startStage, 50, s.now()); err != nil {
			return err
		}
		if err := s.runtime.StartInstance(ctx, instance.RuntimeRef); err != nil {
			return fmt.Errorf("recover start runtime instance: %w", err)
		}
	}
	startedStage := "container_started"
	if instance.ActualIsolation == domain.IsolationVM {
		startedStage = "vm_started"
	}
	if err := s.repo.UpdateOperationStage(ctx, instance.OwnerID, operation.ID, startedStage, 60, s.now()); err != nil {
		return err
	}
	if instance.ActualIsolation == domain.IsolationVM {
		if err := s.waitForVMReady(ctx, instance, operation); err != nil {
			return err
		}
	}
	if _, err := s.Refresh(ctx, instance.OwnerID, instance.ID); err != nil {
		return err
	}
	return s.repo.CompleteOperation(ctx, instance.OwnerID, operation.ID, s.now())
}

func authorizedKeys(owner, gateway string) string {
	owner, gateway = strings.TrimSpace(owner), strings.TrimSpace(gateway)
	if gateway == "" || gateway == owner {
		return owner
	}
	if owner == "" {
		return gateway
	}
	return owner + "\n" + gateway
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
		return domain.Instance{}, fmt.Errorf("inspect runtime instance: %w", err)
	}
	if err := verifyRuntime(runtimeInstance, id, instance.ActualIsolation); err != nil {
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
		if err := s.repo.UpdateOperationStage(ctx, ownerID, op.ID, "starting_instance", 25, s.now()); err != nil {
			return domain.Instance{}, err
		}
		if err := s.runtime.StartInstance(ctx, instance.RuntimeRef); err != nil {
			return domain.Instance{}, fmt.Errorf("start runtime instance: %w", err)
		}
	}
	if err := s.repo.UpdateOperationStage(ctx, ownerID, op.ID, "instance_started", 60, s.now()); err != nil {
		return domain.Instance{}, err
	}
	if instance.ActualIsolation == domain.IsolationVM {
		if err := s.waitForVMReady(ctx, instance, op); err != nil {
			return domain.Instance{}, fmt.Errorf("wait for VM readiness after start: %w", err)
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
		if err := s.repo.UpdateOperationStage(ctx, ownerID, op.ID, "stopping_instance", 25, s.now()); err != nil {
			return domain.Instance{}, err
		}
		if err := s.runtime.StopInstance(ctx, instance.RuntimeRef); err != nil {
			return domain.Instance{}, fmt.Errorf("stop runtime instance: %w", err)
		}
	}
	if err := s.repo.UpdateOperationStage(ctx, ownerID, op.ID, "instance_stopped", 75, s.now()); err != nil {
		return domain.Instance{}, err
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
		if err := s.repo.UpdateOperationStage(ctx, ownerID, op.ID, "restart_stopping", 20, s.now()); err != nil {
			return domain.Instance{}, err
		}
		if err := s.runtime.StopInstance(ctx, instance.RuntimeRef); err != nil {
			return domain.Instance{}, fmt.Errorf("restart stop runtime instance: %w", err)
		}
		if err := s.syncObserved(ctx, instance, domain.ObservedStopped); err != nil {
			return domain.Instance{}, err
		}
		runtimeInstance.State = runtimeapi.StateStopped
		if err := s.repo.UpdateOperationStage(ctx, ownerID, op.ID, "restart_stopped", 35, s.now()); err != nil {
			return domain.Instance{}, err
		}
	}
	if runtimeInstance.State != runtimeapi.StateRunning {
		if err := s.repo.UpdateOperationStage(ctx, ownerID, op.ID, "restart_starting", 50, s.now()); err != nil {
			return domain.Instance{}, err
		}
		if err := s.runtime.StartInstance(ctx, instance.RuntimeRef); err != nil {
			return domain.Instance{}, fmt.Errorf("restart start runtime instance: %w", err)
		}
	}
	if err := s.repo.UpdateOperationStage(ctx, ownerID, op.ID, "restart_started", 60, s.now()); err != nil {
		return domain.Instance{}, err
	}
	if instance.ActualIsolation == domain.IsolationVM {
		if err := s.waitForVMReady(ctx, instance, op); err != nil {
			return domain.Instance{}, fmt.Errorf("wait for VM readiness after restart: %w", err)
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
		if err := verifyRuntime(runtimeInstance, id, instance.ActualIsolation); err != nil {
			return err
		}
	}
	if instance.Protected {
		return &domain.Error{Code: domain.CodeProtectedBase, Field: "desired_state"}
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
		if err := s.repo.UpdateOperationStage(ctx, ownerID, op.ID, "delete_stopping", 20, s.now()); err != nil {
			return err
		}
		if err := s.runtime.StopInstance(ctx, instance.RuntimeRef); err != nil {
			return fmt.Errorf("stop runtime instance for deletion: %w", err)
		}
		if err := s.repo.UpdateOperationStage(ctx, ownerID, op.ID, "delete_stopped", 35, s.now()); err != nil {
			return err
		}
	}
	if exists {
		if err := s.repo.UpdateOperationStage(ctx, ownerID, op.ID, "deleting_runtime", 60, s.now()); err != nil {
			return err
		}
		if err := s.runtime.DeleteInstance(ctx, instance.RuntimeRef); err != nil && !errors.Is(err, runtimeapi.ErrNotFound) {
			return fmt.Errorf("delete runtime instance: %w", err)
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
	if err := s.repo.UpdateOperationStage(ctx, ownerID, op.ID, "runtime_deleted", 80, s.now()); err != nil {
		return err
	}
	if err := s.repo.UpdateInstanceObservation(ctx, ownerID, id, instance.RuntimeRef, instance.ActualIsolation, domain.ObservedDeleted, "", s.now()); err != nil {
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
	if err := verifyRuntime(runtimeInstance, id, instance.ActualIsolation); err != nil {
		return domain.Instance{}, runtimeapi.Instance{}, err
	}
	return instance, runtimeInstance, nil
}

func (s *Service) recordRuntimeMissing(ctx context.Context, instance domain.Instance, cause error) error {
	if err := s.repo.UpdateInstanceObservation(ctx, instance.OwnerID, instance.ID, instance.RuntimeRef, instance.ActualIsolation, domain.ObservedError, domain.CodeRuntimeMissing, s.now()); err != nil {
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
		if err := s.repo.UpdateInstanceObservation(ctx, instance.OwnerID, instance.ID, instance.RuntimeRef, instance.ActualIsolation, next, "", s.now()); err != nil {
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
	_ = s.repo.UpdateInstanceObservation(ctx, instance.OwnerID, instance.ID, instance.RuntimeRef, instance.ActualIsolation, domain.ObservedError, domain.ErrorCode("runtime_error"), s.now())
}

func selectIsolation(request domain.IsolationRequest, capabilities runtimeapi.Capabilities) (domain.IsolationType, error) {
	vmUsable := capabilities.VirtualMachines && capabilities.VMAvailability == runtimeapi.VMAvailable
	switch request {
	case domain.IsolationStandard:
		if !capabilities.Containers {
			return "", &CapabilityError{Capability: "containers"}
		}
		return domain.IsolationContainer, nil
	case domain.IsolationStrong:
		if !vmUsable {
			reason := capabilities.VMReason
			if reason == "" {
				reason = string(capabilities.VMAvailability)
			}
			return "", &CapabilityError{Capability: "strong_isolation", Reason: reason}
		}
		return domain.IsolationVM, nil
	case domain.IsolationBestAvailable:
		if vmUsable {
			return domain.IsolationVM, nil
		}
		if !capabilities.Containers {
			return "", &CapabilityError{Capability: "containers", Reason: "VM capability is also unavailable"}
		}
		return domain.IsolationContainer, nil
	default:
		return "", &domain.Error{Code: domain.CodeInvalidArgument, Field: "requested_isolation"}
	}
}

func (s *Service) waitForVMReady(ctx context.Context, instance domain.Instance, operation domain.Operation) error {
	progress := map[string]int{"waiting_for_agent": 65, "waiting_for_ssh": 85}
	return s.runtime.WaitInstanceReady(ctx, runtimeapi.ReadinessRequest{Ref: instance.RuntimeRef, Stage: func(stage string) error {
		value, ok := progress[stage]
		if !ok {
			return fmt.Errorf("unknown VM readiness stage %q", stage)
		}
		return s.repo.UpdateOperationStage(ctx, instance.OwnerID, operation.ID, stage, value, s.now())
	}})
}

func (s *Service) withPartialVMCleanup(instance domain.Instance, created bool, cause error) error {
	if !created || instance.ActualIsolation != domain.IsolationVM {
		return cause
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runtimeInstance, exists, err := s.inspectOptional(ctx, instance.RuntimeRef)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("inspect partial VM for cleanup: %w", err))
	}
	if !exists {
		return cause
	}
	if err := verifyRuntime(runtimeInstance, instance.ID, domain.IsolationVM); err != nil {
		return errors.Join(cause, fmt.Errorf("refuse partial VM cleanup after identity change: %w", err))
	}
	if runtimeInstance.State == runtimeapi.StateRunning {
		if err := s.runtime.StopInstance(ctx, instance.RuntimeRef); err != nil && !errors.Is(err, runtimeapi.ErrNotFound) {
			return errors.Join(cause, fmt.Errorf("stop partial VM: %w", err))
		}
	}
	runtimeInstance, exists, err = s.inspectOptional(ctx, instance.RuntimeRef)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("re-inspect partial VM before deletion: %w", err))
	}
	if !exists {
		return cause
	}
	if err := verifyRuntime(runtimeInstance, instance.ID, domain.IsolationVM); err != nil {
		return errors.Join(cause, fmt.Errorf("refuse partial VM deletion after identity change: %w", err))
	}
	if err := s.runtime.DeleteInstance(ctx, instance.RuntimeRef); err != nil && !errors.Is(err, runtimeapi.ErrNotFound) {
		return errors.Join(cause, fmt.Errorf("delete partial VM: %w", err))
	}
	remaining, stillExists, err := s.inspectOptional(ctx, instance.RuntimeRef)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("verify partial VM cleanup: %w", err))
	}
	if stillExists {
		if err := verifyRuntime(remaining, instance.ID, domain.IsolationVM); err != nil {
			return errors.Join(cause, fmt.Errorf("partial VM was replaced during cleanup: %w", err))
		}
		return errors.Join(cause, fmt.Errorf("partial VM %q still exists after cleanup", instance.RuntimeRef))
	}
	return cause
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

func verifyRuntime(instance runtimeapi.Instance, expected domain.InstanceID, isolation domain.IsolationType) error {
	if err := verifyIdentity(instance, expected); err != nil {
		return err
	}
	switch isolation {
	case domain.IsolationVM:
		if !instance.IsVM {
			return &CapabilityError{Capability: "virtual_machine", Reason: "runtime returned a container"}
		}
	case domain.IsolationContainer:
		if instance.IsVM {
			return &CapabilityError{Capability: "container", Reason: "runtime returned a virtual machine"}
		}
		if instance.Privileged {
			return &CapabilityError{Capability: "unprivileged_container", Reason: "runtime returned a privileged container"}
		}
	default:
		return &CapabilityError{Capability: "actual_isolation", Reason: string(isolation)}
	}
	return nil
}

// VerifyRuntimeIdentity rejects replacement resources and isolation drift.
func VerifyRuntimeIdentity(instance runtimeapi.Instance, expected domain.InstanceID, isolation domain.IsolationType) error {
	return verifyRuntime(instance, expected, isolation)
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

// applyCreateDefaults fills kind-specific image, isolation, resource, and
// lifetime gaps. Curated aliases are identical across arch/runtime, so a
// container/x86_64 catalog lookup is enough before capabilities are known.
func applyCreateDefaults(input *CreateInput) (time.Duration, error) {
	applied, err := sandbox.ApplyDefaults(sandbox.CreateDefaults{
		Kind:               input.Kind,
		Architecture:       "x86_64",
		Runtime:            "container",
		Catalog:            images.DefaultCatalog(),
		Image:              input.Image,
		RequestedIsolation: input.RequestedIsolation,
		Resources:          input.Resources,
		Lifetime:           input.Lifetime,
	})
	if err != nil {
		return 0, err
	}
	input.Kind = coalesceKind(input.Kind)
	input.Image = applied.Image
	input.RequestedIsolation = applied.RequestedIsolation
	input.Resources = applied.Resources
	input.Lifetime = applied.Lifetime
	return applied.Lifetime, nil
}

func coalesceKind(kind domain.InstanceKind) domain.InstanceKind {
	if kind == "" {
		return domain.KindVPS
	}
	return kind
}

func applyLifetime(instance *domain.Instance, lifetime time.Duration, now time.Time) {
	if lifetime <= 0 || instance.Kind != domain.KindSandbox {
		return
	}
	expires := now.Add(lifetime)
	instance.ExpiresAt = &expires
}

func randomID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return strings.ToLower(hex.EncodeToString(value))
}
