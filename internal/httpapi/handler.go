// SPDX-License-Identifier: AGPL-3.0-only

// Package httpapi exposes the versioned OpenBox HTTP transport without leaking
// transport types into the domain or application packages.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/auth"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/httpapi/generated"
	"github.com/openbox-dev/openbox/internal/images"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/version"
)

const (
	APIVersion           = "v1"
	HeaderAPIVersion     = "X-OpenBox-API-Version"
	HeaderIdempotencyKey = "Idempotency-Key"
	DefaultAddress       = "127.0.0.1:8443"
	defaultMaxBodyBytes  = int64(1 << 20)
	defaultEventLimit    = 100
)

type Action = instances.MutationAction

const (
	ActionStart   = instances.MutationStart
	ActionStop    = instances.MutationStop
	ActionRestart = instances.MutationRestart
	ActionDelete  = instances.MutationDelete
)

// Service is the transport-facing application boundary. Implementations own
// authorization and persistence; Handler always supplies its configured owner.
type Service interface {
	Health(context.Context) error
	Capabilities(context.Context) (runtimeapi.Capabilities, error)
	ListImages(context.Context, domain.OwnerID) ([]domain.Image, error)
	ListInstances(context.Context, domain.OwnerID) ([]domain.Instance, error)
	GetInstance(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error)
	SubmitCreate(context.Context, instances.CreateInput) (domain.Instance, domain.Operation, error)
	SubmitAction(context.Context, domain.OwnerID, domain.InstanceID, instances.MutationAction, string) (domain.Operation, error)
	ListOperations(context.Context, domain.OwnerID) ([]domain.Operation, error)
	GetOperation(context.Context, domain.OwnerID, domain.OperationID) (domain.Operation, error)
	ListOperationEventsAfter(context.Context, domain.OwnerID, domain.OperationID, int, int) ([]domain.OperationEvent, error)
	CancelOperation(context.Context, domain.OwnerID, domain.OperationID) (domain.Operation, error)
}

type Options struct {
	OwnerID           domain.OwnerID
	Auth              *auth.Manager
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	MaxBodyBytes      int64
	EventBatchSize    int
}

type Handler struct {
	service           Service
	fixedOwnerID      domain.OwnerID
	auth              *auth.Manager
	pollInterval      time.Duration
	heartbeatInterval time.Duration
	maxBodyBytes      int64
	eventBatchSize    int
}

func New(service Service, options Options) (*Handler, error) {
	if service == nil {
		return nil, errors.New("HTTP API service is required")
	}
	if options.OwnerID == "" && options.Auth == nil {
		return nil, errors.New("HTTP API owner is required")
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 250 * time.Millisecond
	}
	if options.HeartbeatInterval <= 0 {
		options.HeartbeatInterval = 15 * time.Second
	}
	if options.MaxBodyBytes <= 0 {
		options.MaxBodyBytes = defaultMaxBodyBytes
	}
	if options.EventBatchSize <= 0 {
		options.EventBatchSize = defaultEventLimit
	}
	return &Handler{
		service: service, fixedOwnerID: options.OwnerID, auth: options.Auth, pollInterval: options.PollInterval,
		heartbeatInterval: options.HeartbeatInterval, maxBodyBytes: options.MaxBodyBytes,
		eventBatchSize: options.EventBatchSize,
	}, nil
}

func NewServer(address string, handler http.Handler) *http.Server {
	if address == "" {
		address = DefaultAddress
	}
	return &http.Server{
		Addr: address, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13},
	}
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	requestID := newRequestID()
	response.Header().Set(HeaderAPIVersion, APIVersion)
	response.Header().Set("X-Request-ID", requestID)
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Cache-Control", "no-store")
	if requested := request.Header.Get(HeaderAPIVersion); requested != "" && requested != APIVersion {
		h.writeError(response, requestID, http.StatusUpgradeRequired, "unsupported_api_version", HeaderAPIVersion)
		return
	}

	segments := splitPath(request.URL.Path)
	if len(segments) < 2 || segments[0] != "v1" {
		h.writeError(response, requestID, http.StatusNotFound, string(domain.CodeNotFound), "path")
		return
	}
	if h.auth != nil && !isPublicAuthRoute(segments, request.Method) {
		var err error
		request, err = h.authenticate(request)
		if err != nil {
			status, code := http.StatusUnauthorized, "unauthenticated"
			if errors.Is(err, auth.ErrForbidden) {
				status, code = http.StatusForbidden, "forbidden"
			}
			h.writeError(response, requestID, status, code, "authorization")
			return
		}
	}

	switch segments[1] {
	case "health":
		if len(segments) == 2 && h.requireMethod(response, request, requestID, http.MethodGet) {
			h.health(response, request, requestID)
			return
		}
	case "bootstrap":
		if len(segments) == 2 && h.routeBootstrap(response, request, requestID) {
			return
		}
	case "sessions":
		if len(segments) == 2 && h.routeSessions(response, request, requestID) {
			return
		}
	case "session":
		if h.routeSession(response, request, requestID, segments[2:]) {
			return
		}
	case "tokens":
		if h.routeTokens(response, request, requestID, segments[2:]) {
			return
		}
	case "ssh-keys":
		if h.routeSSHKeys(response, request, requestID, segments[2:]) {
			return
		}
	case "capabilities":
		if len(segments) == 2 && h.requireMethod(response, request, requestID, http.MethodGet) {
			h.capabilities(response, request, requestID)
			return
		}
	case "images":
		if len(segments) == 2 && h.requireMethod(response, request, requestID, http.MethodGet) {
			h.listImages(response, request, requestID)
			return
		}
	case "instances":
		if h.routeInstances(response, request, requestID, segments[2:]) {
			return
		}
	case "operations":
		if h.routeOperations(response, request, requestID, segments[2:]) {
			return
		}
	}
	h.writeError(response, requestID, http.StatusNotFound, string(domain.CodeNotFound), "path")
}

func (h *Handler) routeInstances(response http.ResponseWriter, request *http.Request, requestID string, rest []string) bool {
	if len(rest) == 0 {
		switch request.Method {
		case http.MethodGet:
			h.listInstances(response, request, requestID)
		case http.MethodPost:
			h.createInstance(response, request, requestID)
		default:
			h.methodNotAllowed(response, requestID, http.MethodGet, http.MethodPost)
		}
		return true
	}
	if len(rest) == 1 {
		switch request.Method {
		case http.MethodGet:
			h.getInstance(response, request, requestID, rest[0])
		case http.MethodDelete:
			h.submitAction(response, request, requestID, rest[0], ActionDelete)
		default:
			h.methodNotAllowed(response, requestID, http.MethodGet, http.MethodDelete)
		}
		return true
	}
	if len(rest) == 2 && rest[1] == "terminal" {
		if !h.requireMethod(response, request, requestID, http.MethodGet) {
			return true
		}
		h.openTerminal(response, request, requestID, rest[0])
		return true
	}
	if len(rest) == 3 && rest[1] == "actions" {
		if !h.requireMethod(response, request, requestID, http.MethodPost) {
			return true
		}
		action := Action(rest[2])
		if action != ActionStart && action != ActionStop && action != ActionRestart {
			h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "action")
			return true
		}
		h.submitAction(response, request, requestID, rest[0], action)
		return true
	}
	return false
}

func (h *Handler) routeOperations(response http.ResponseWriter, request *http.Request, requestID string, rest []string) bool {
	if len(rest) == 0 {
		if h.requireMethod(response, request, requestID, http.MethodGet) {
			h.listOperations(response, request, requestID)
		}
		return true
	}
	if len(rest) == 1 {
		if h.requireMethod(response, request, requestID, http.MethodGet) {
			h.getOperation(response, request, requestID, rest[0])
		}
		return true
	}
	if len(rest) == 2 && rest[1] == "cancel" {
		if h.requireMethod(response, request, requestID, http.MethodPost) {
			h.cancelOperation(response, request, requestID, rest[0])
		}
		return true
	}
	if len(rest) == 2 && rest[1] == "events" {
		if h.requireMethod(response, request, requestID, http.MethodGet) {
			h.streamOperationEvents(response, request, requestID, rest[0])
		}
		return true
	}
	return false
}

func (h *Handler) health(response http.ResponseWriter, request *http.Request, requestID string) {
	if err := h.service.Health(request.Context()); err != nil {
		h.writeError(response, requestID, http.StatusServiceUnavailable, "unavailable", "")
		return
	}
	h.writeJSON(response, http.StatusOK, generated.Health{Status: generated.Ok, ApiVersion: generated.HealthApiVersionV1, ServerVersion: version.Version})
}

func (h *Handler) capabilities(response http.ResponseWriter, request *http.Request, requestID string) {
	value, err := h.service.Capabilities(request.Context())
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, mapCapabilities(value))
}

func (h *Handler) listImages(response http.ResponseWriter, request *http.Request, requestID string) {
	values, err := h.service.ListImages(request.Context(), h.requestOwner(request))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	items := make([]generated.Image, 0, len(values))
	for _, value := range values {
		items = append(items, mapImage(value))
	}
	h.writeJSON(response, http.StatusOK, generated.ListImagesResponse{Items: items})
}

func (h *Handler) listInstances(response http.ResponseWriter, request *http.Request, requestID string) {
	values, err := h.service.ListInstances(request.Context(), h.requestOwner(request))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	items := make([]generated.Instance, 0, len(values))
	for _, value := range values {
		items = append(items, mapInstance(value))
	}
	h.writeJSON(response, http.StatusOK, generated.ListInstancesResponse{Items: items})
}

func (h *Handler) getInstance(response http.ResponseWriter, request *http.Request, requestID, rawID string) {
	value, err := h.service.GetInstance(request.Context(), h.requestOwner(request), domain.InstanceID(rawID))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, mapInstance(value))
}

func (h *Handler) createInstance(response http.ResponseWriter, request *http.Request, requestID string) {
	key := request.Header.Get(HeaderIdempotencyKey)
	if key == "" || len(key) > 255 {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), HeaderIdempotencyKey)
		return
	}
	var input generated.CreateInstanceRequest
	if err := h.decodeJSON(response, request, &input); err != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	var kind domain.InstanceKind
	if input.Kind != nil {
		kind = domain.InstanceKind(*input.Kind)
	}
	var isolation domain.IsolationRequest
	if input.RequestedIsolation != nil {
		isolation = domain.IsolationRequest(*input.RequestedIsolation)
	}
	var resources domain.Resources
	if input.Resources != nil {
		resources = domain.Resources{VCPUs: input.Resources.Vcpus, MemoryBytes: input.Resources.MemoryBytes, DiskBytes: input.Resources.DiskBytes}
	}
	instance, operation, err := h.service.SubmitCreate(request.Context(), instances.CreateInput{
		OwnerID: h.requestOwner(request), Name: input.Name, Kind: kind, Image: input.Image,
		RequestedIsolation: isolation,
		Resources:          resources,
		OwnerPublicKey:     input.OwnerPublicKey, IdempotencyKey: key,
	})
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	result := generated.CreateInstanceResult{Operation: mapOperation(operation)}
	if instance.ID != "" {
		mapped := mapInstance(instance)
		result.Instance = &mapped
	}
	h.writeJSON(response, http.StatusAccepted, result)
}

func (h *Handler) submitAction(response http.ResponseWriter, request *http.Request, requestID, rawID string, action Action) {
	key := request.Header.Get(HeaderIdempotencyKey)
	if key == "" || len(key) > 255 {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), HeaderIdempotencyKey)
		return
	}
	operation, err := h.service.SubmitAction(request.Context(), h.requestOwner(request), domain.InstanceID(rawID), action, key)
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusAccepted, mapOperation(operation))
}

func (h *Handler) listOperations(response http.ResponseWriter, request *http.Request, requestID string) {
	values, err := h.service.ListOperations(request.Context(), h.requestOwner(request))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	items := make([]generated.Operation, 0, len(values))
	for _, value := range values {
		items = append(items, mapOperation(value))
	}
	h.writeJSON(response, http.StatusOK, generated.ListOperationsResponse{Items: items})
}

func (h *Handler) getOperation(response http.ResponseWriter, request *http.Request, requestID, rawID string) {
	value, err := h.service.GetOperation(request.Context(), h.requestOwner(request), domain.OperationID(rawID))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, mapOperation(value))
}

func (h *Handler) cancelOperation(response http.ResponseWriter, request *http.Request, requestID, rawID string) {
	value, err := h.service.CancelOperation(request.Context(), h.requestOwner(request), domain.OperationID(rawID))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, mapOperation(value))
}

func (h *Handler) decodeJSON(response http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(response, request.Body, h.maxBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON value")
	}
	return nil
}

func (h *Handler) requireMethod(response http.ResponseWriter, request *http.Request, requestID string, methods ...string) bool {
	for _, method := range methods {
		if request.Method == method {
			return true
		}
	}
	h.methodNotAllowed(response, requestID, methods...)
	return false
}

func (h *Handler) methodNotAllowed(response http.ResponseWriter, requestID string, methods ...string) {
	response.Header().Set("Allow", strings.Join(methods, ", "))
	h.writeError(response, requestID, http.StatusMethodNotAllowed, "method_not_allowed", "method")
}

func (h *Handler) writeServiceError(response http.ResponseWriter, requestID string, err error) {
	status, code, field := classifyError(err)
	h.writeError(response, requestID, status, code, field)
}

func (h *Handler) writeError(response http.ResponseWriter, requestID string, status int, code, field string) {
	h.writeJSON(response, status, errorEnvelope(requestID, code, publicMessage(code), field))
}

func (h *Handler) writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func classifyError(err error) (int, string, string) {
	var domainError *domain.Error
	if errors.As(err, &domainError) {
		switch domainError.Code {
		case domain.CodeInvalidArgument, domain.CodeExpiryRequired:
			return http.StatusBadRequest, string(domainError.Code), domainError.Field
		case domain.CodeNotFound:
			return http.StatusNotFound, string(domainError.Code), domainError.Field
		case domain.CodeConflict, domain.CodeInvalidTransition, domain.CodeProtectedBase, domain.CodeIdempotencyConflict, domain.CodeRuntimeMissing, domain.CodeCancellationUnsafe:
			return http.StatusConflict, string(domainError.Code), domainError.Field
		case domain.CodeUnavailable:
			return http.StatusServiceUnavailable, string(domainError.Code), domainError.Field
		default:
			return http.StatusInternalServerError, string(domainError.Code), domainError.Field
		}
	}
	var capabilityError *instances.CapabilityError
	if errors.As(err, &capabilityError) {
		return http.StatusUnprocessableEntity, "capability_unavailable", capabilityError.Capability
	}
	var notFound *images.NotFoundError
	if errors.As(err, &notFound) {
		return http.StatusNotFound, string(domain.CodeNotFound), "image"
	}
	var ambiguous *images.AmbiguousError
	if errors.As(err, &ambiguous) {
		return http.StatusConflict, string(domain.CodeConflict), "image"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "timeout", ""
	}
	return http.StatusInternalServerError, "internal", ""
}

func publicMessage(code string) string {
	switch code {
	case string(domain.CodeInvalidArgument):
		return "The request is invalid."
	case string(domain.CodeNotFound):
		return "The requested resource was not found."
	case string(domain.CodeConflict), string(domain.CodeInvalidTransition), string(domain.CodeProtectedBase), string(domain.CodeIdempotencyConflict), string(domain.CodeRuntimeMissing), string(domain.CodeCancellationUnsafe):
		return "The request conflicts with the current resource state."
	case string(domain.CodeUnavailable):
		return "The service is temporarily unavailable."
	case "unsupported_api_version":
		return "The requested API version is not supported."
	case "method_not_allowed":
		return "The method is not allowed for this resource."
	case "capability_unavailable":
		return "The requested runtime capability is unavailable."
	case "timeout":
		return "The request timed out."
	default:
		return "The request could not be completed."
	}
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func newRequestID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(value[:])
}

func mapCapabilities(value runtimeapi.Capabilities) generated.Capabilities {
	return generated.Capabilities{
		Architecture: value.Architecture, IncusVersion: value.IncusVersion,
		Namespaces: nonNilMap(value.Namespaces), Cgroups: value.Cgroups,
		StorageDrivers: nonNilSlice(value.StorageDrivers), NetworkTools: nonNilMap(value.NetworkTools),
		Kvm: value.KVM, Containers: value.Containers, VirtualMachines: value.VirtualMachines,
		VmAvailability: string(value.VMAvailability), VmReason: optionalString(value.VMReason),
	}
}

func mapImage(value domain.Image) generated.Image {
	return generated.Image{Id: string(value.ID), Alias: value.Alias, Source: value.Source, Digest: value.Digest, Architecture: value.Architecture, Compatibility: value.Compatibility, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func mapInstance(value domain.Instance) generated.Instance {
	return generated.Instance{
		Id: string(value.ID), Name: value.Name, Kind: generated.InstanceKind(value.Kind), ImageId: string(value.ImageID),
		RequestedIsolation: generated.InstanceRequestedIsolation(value.RequestedIsolation), ActualIsolation: generated.InstanceActualIsolation(value.ActualIsolation),
		DesiredState: generated.InstanceDesiredState(value.DesiredState), ObservedState: generated.InstanceObservedState(value.ObservedState),
		Resources: generated.Resources{Vcpus: value.Resources.VCPUs, MemoryBytes: value.Resources.MemoryBytes, DiskBytes: value.Resources.DiskBytes},
		ExpiresAt: value.ExpiresAt, Protected: value.Protected, ErrorCode: optionalString(string(value.ErrorCode)), ErrorStage: optionalString(value.ErrorStage),
		ErrorRetryable: pointer(value.ErrorRetryable), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func mapOperation(value domain.Operation) generated.Operation {
	return generated.Operation{
		Id: string(value.ID), Type: value.Type, TargetType: value.TargetType, TargetId: value.TargetID,
		Status: generated.OperationStatus(value.Status), Stage: value.Stage, Progress: value.Progress, ErrorCode: optionalString(string(value.ErrorCode)),
		ErrorClass: optionalString(value.ErrorClass), Attempts: value.Attempts, NextAttemptAt: value.NextAttemptAt,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func errorEnvelope(requestID, code, message, field string) generated.ErrorEnvelope {
	var result generated.ErrorEnvelope
	result.Error.Code = code
	result.Error.Message = message
	result.Error.Retryable = code == string(domain.CodeUnavailable) || code == "timeout"
	result.Error.RequestId = requestID
	result.Error.Field = optionalString(field)
	return result
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func pointer[T any](value T) *T { return &value }

func nonNilMap(value map[string]bool) map[string]bool {
	if value == nil {
		return map[string]bool{}
	}
	return value
}

func nonNilSlice(value []string) []string {
	if value == nil {
		return []string{}
	}
	return value
}

var _ Service = (*instances.Service)(nil)
