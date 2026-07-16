// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/execstream"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/sandbox"
)

func TestHealthAndCompatibilityNegotiation(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, &fakeService{})

	t.Run("advertises API version", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
		if got := response.Header().Get(HeaderAPIVersion); got != APIVersion {
			t.Fatalf("%s = %q", HeaderAPIVersion, got)
		}
		assertJSONContains(t, response.Body.Bytes(), `"status":"ok"`, `"api_version":"v1"`)
	})

	t.Run("rejects unsupported requested version", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
		request.Header.Set(HeaderAPIVersion, "v2")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusUpgradeRequired {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
		assertJSONContains(t, response.Body.Bytes(), `"code":"unsupported_api_version"`, `"request_id":`)
	})
}

func TestSoftwareCatalogAndInstall(t *testing.T) {
	t.Parallel()
	service := &fakeService{
		instances: []domain.Instance{{ID: "instance-1", OwnerID: "owner-local", Name: "dev", Kind: domain.KindVPS, ObservedState: domain.ObservedRunning}},
		installedSoftware: domain.InstanceSoftware{
			InstanceID: "instance-1", OwnerID: "owner-local", PackageID: "pi", Status: domain.SoftwareInstalled, Version: "0.80.7",
			UpdatedAt: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		},
	}
	handler := newTestHandler(t, service)

	catalog := httptest.NewRecorder()
	handler.ServeHTTP(catalog, httptest.NewRequest(http.MethodGet, "/v1/software", nil))
	if catalog.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", catalog.Code, catalog.Body.String())
	}
	assertJSONContains(t, catalog.Body.Bytes(), `"id":"pi"`)
	assertJSONContains(t, catalog.Body.Bytes(), `"id":"herdr"`)

	install := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/instances/instance-1/software/pi/install", nil)
	handler.ServeHTTP(install, req)
	if install.Code != http.StatusOK {
		t.Fatalf("install status=%d body=%s", install.Code, install.Body.String())
	}
	assertJSONContains(t, install.Body.Bytes(), `"package_id":"pi"`, `"status":"installed"`)
	if service.installPackageID != "pi" {
		t.Fatalf("install package=%q", service.installPackageID)
	}
}

func TestCapabilitiesImagesAndInstancesUseFixedOwner(t *testing.T) {
	t.Parallel()

	service := &fakeService{
		capabilities: runtimeapi.Capabilities{Architecture: "x86_64", Containers: true, KVM: true, VirtualMachines: true, VMAvailability: runtimeapi.VMAvailable},
		images:       []domain.Image{{ID: "img-1", OwnerID: "owner-local", Alias: "ubuntu", Digest: "sha256:abc"}},
		instances: []domain.Instance{{
			ID: "instance-1", OwnerID: "owner-local", Name: "dev", Kind: domain.KindVPS,
			NetworkPolicy: domain.NetworkPolicyStatus{
				EgressMode: domain.EgressStandard, ACLs: []string{"openbox-default-deny", "openbox-egress-standard"}, DeniedFlows: 2,
				Resolution: domain.AllowlistResolution{State: "idle", Pending: []string{}, Resolved: []string{}, Failed: []string{}},
			},
		}},
	}
	handler := newTestHandler(t, service)

	for _, path := range []string{"/v1/capabilities", "/v1/images", "/v1/instances", "/v1/instances/instance-1"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-OpenBox-Owner", "attacker")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", path, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/instances/instance-1", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertJSONContains(t, response.Body.Bytes(), `"egress_mode":"standard"`, `"acls":["openbox-default-deny","openbox-egress-standard"]`, `"denied_flows":2`, `"state":"idle"`)
	if service.lastOwner != "owner-local" {
		t.Fatalf("owner = %q, want fixed owner-local", service.lastOwner)
	}
}

func TestCreateRequiresIdempotencyAndReturnsOperation(t *testing.T) {
	t.Parallel()

	service := &fakeService{
		created:   domain.Instance{ID: "instance-1", OwnerID: "owner-local", Name: "dev", Kind: domain.KindVPS},
		operation: domain.Operation{ID: "operation-1", OwnerID: "owner-local", Status: domain.OperationPending},
	}
	handler := newTestHandler(t, service)
	body := []byte(`{"name":"dev","kind":"vps","image":"ubuntu","owner_public_key":"ssh-ed25519 AAAA","requested_isolation":"strong","resources":{"vcpus":2,"memory_bytes":4294967296,"disk_bytes":21474836480}}`)

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodPost, "/v1/instances", bytes.NewReader(body)))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing key status = %d", missing.Code)
	}
	assertJSONContains(t, missing.Body.Bytes(), `"code":"invalid_argument"`, `"field":"Idempotency-Key"`)

	request := httptest.NewRequest(http.MethodPost, "/v1/instances", bytes.NewReader(body))
	request.Header.Set(HeaderIdempotencyKey, "create-dev")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	assertJSONContains(t, response.Body.Bytes(), `"id":"instance-1"`, `"id":"operation-1"`)
	if service.createInput.OwnerID != "owner-local" || service.createInput.IdempotencyKey != "create-dev" {
		t.Fatalf("create input = %#v", service.createInput)
	}
}

func TestMutationActionsAndErrorEnvelope(t *testing.T) {
	t.Parallel()

	service := &fakeService{operation: domain.Operation{ID: "operation-1", OwnerID: "owner-local", Status: domain.OperationPending}}
	handler := newTestHandler(t, service)

	request := httptest.NewRequest(http.MethodPost, "/v1/instances/instance-1/actions/start", nil)
	request.Header.Set(HeaderIdempotencyKey, "start-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || service.action != ActionStart {
		t.Fatalf("status = %d action = %q body = %s", response.Code, service.action, response.Body.String())
	}

	service.err = &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	notFound := httptest.NewRecorder()
	handler.ServeHTTP(notFound, httptest.NewRequest(http.MethodGet, "/v1/instances/missing", nil))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", notFound.Code, notFound.Body.String())
	}
	assertJSONContains(t, notFound.Body.Bytes(), `"code":"not_found"`, `"field":"instance"`)
	if strings.Contains(notFound.Body.String(), "cause") {
		t.Fatalf("response leaked internal error: %s", notFound.Body.String())
	}
}

func TestUnavailableAndUnsafeCancellationHaveStableErrors(t *testing.T) {
	service := &fakeService{err: &domain.Error{Code: domain.CodeUnavailable, Field: "runtime"}}
	handler := newTestHandler(t, service)
	unavailable := httptest.NewRecorder()
	handler.ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/v1/instances", nil))
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", unavailable.Code, unavailable.Body.String())
	}
	assertJSONContains(t, unavailable.Body.Bytes(), `"code":"unavailable"`, `"retryable":true`)

	service.err = &domain.Error{Code: domain.CodeCancellationUnsafe, Field: "operation.stage"}
	unsafe := httptest.NewRecorder()
	handler.ServeHTTP(unsafe, httptest.NewRequest(http.MethodPost, "/v1/operations/operation-1/cancel", nil))
	if unsafe.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", unsafe.Code, unsafe.Body.String())
	}
	assertJSONContains(t, unsafe.Body.Bytes(), `"code":"cancellation_unsafe"`, `"retryable":false`)
}

func TestDeleteAndOperationEndpoints(t *testing.T) {
	t.Parallel()

	operation := domain.Operation{ID: "operation-1", OwnerID: "owner-local", Status: domain.OperationPending}
	service := &fakeService{operation: operation, operations: []domain.Operation{operation}}
	handler := newTestHandler(t, service)

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/v1/instances/instance-1", nil)
	deleteRequest.Header.Set(HeaderIdempotencyKey, "delete-1")
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusAccepted || service.action != ActionDelete {
		t.Fatalf("delete status = %d action = %q body = %s", deleteResponse.Code, service.action, deleteResponse.Body.String())
	}

	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/operations"},
		{http.MethodGet, "/v1/operations/operation-1"},
		{http.MethodPost, "/v1/operations/operation-1/cancel"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s: status = %d body = %s", test.method, test.path, response.Code, response.Body.String())
		}
		assertJSONContains(t, response.Body.Bytes(), `"id":"operation-1"`)
	}
	if !service.canceled {
		t.Fatal("cancel endpoint did not call service")
	}
}

func TestCancellationAndDegradedModeErrorsAreStable(t *testing.T) {
	t.Parallel()

	service := &fakeService{}
	handler := newTestHandler(t, service)

	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{&domain.Error{Code: domain.CodeCancellationUnsafe, Field: "operation.stage"}, http.StatusConflict, "cancellation_unsafe"},
		{&domain.Error{Code: domain.CodeUnavailable, Field: "operations.mode"}, http.StatusServiceUnavailable, "unavailable"},
	} {
		service.err = test.err
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/operations/operation-1/cancel", nil))
		if response.Code != test.status {
			t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
		}
		assertJSONContains(t, response.Body.Bytes(), `"code":"`+test.code+`"`)
	}
}

func TestOperationEventStreamReplaysThenClosesOnTerminalEvent(t *testing.T) {
	service := &fakeService{
		operation: domain.Operation{ID: "operation-1", OwnerID: "owner-local", Status: domain.OperationRunning},
		eventBatches: [][]domain.OperationEvent{
			{{Sequence: 2, OperationID: "operation-1", OwnerID: "owner-local", Stage: "starting", Status: domain.OperationRunning, CreatedAt: time.Unix(2, 0).UTC()}},
			{{Sequence: 3, OperationID: "operation-1", OwnerID: "owner-local", Stage: "complete", Status: domain.OperationSucceeded, CreatedAt: time.Unix(3, 0).UTC()}},
		},
	}
	handler := newTestHandlerWithOptions(t, service, Options{OwnerID: "owner-local", PollInterval: time.Millisecond, HeartbeatInterval: time.Hour})
	request := httptest.NewRequest(http.MethodGet, "/v1/operations/operation-1/events", nil)
	request.Header.Set("Last-Event-ID", "1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type = %q", got)
	}
	body := response.Body.String()
	for _, fragment := range []string{"id: 2", "event: operation", `"stage":"starting"`, "id: 3", `"status":"succeeded"`} {
		if !strings.Contains(body, fragment) {
			t.Errorf("stream missing %q: %s", fragment, body)
		}
	}
	if service.eventAfter[0] != 1 || service.eventAfter[1] != 2 {
		t.Fatalf("after cursors = %v", service.eventAfter)
	}
}

func TestOperationEventStreamHeartbeatAndCancellation(t *testing.T) {
	service := &fakeService{operation: domain.Operation{ID: "operation-1", OwnerID: "owner-local", Status: domain.OperationRunning}}
	handler := newTestHandlerWithOptions(t, service, Options{OwnerID: "owner-local", PollInterval: time.Hour, HeartbeatInterval: time.Millisecond})

	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/operations/operation-1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, ": heartbeat ") {
		t.Fatalf("heartbeat = %q", line)
	}
	cancel()
	_ = response.Body.Close()
}

func TestPrivateSafeServerDefaults(t *testing.T) {
	t.Parallel()

	server := NewServer("", newTestHandler(t, &fakeService{}))
	if server.Addr != DefaultAddress {
		t.Fatalf("address = %q", server.Addr)
	}
	if server.ReadHeaderTimeout <= 0 || server.IdleTimeout <= 0 || server.MaxHeaderBytes <= 0 {
		t.Fatalf("unsafe server defaults: %#v", server)
	}
	if server.TLSConfig == nil || server.TLSConfig.MinVersion == 0 {
		t.Fatalf("missing TLS minimum version")
	}
}

func TestMalformedJSONAndUnknownRoutesAreJSONErrors(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t, &fakeService{})
	request := httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{"name":`))
	request.Header.Set(HeaderIdempotencyKey, "x")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}

	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/v1/nope", nil))
	if unknown.Code != http.StatusNotFound || !strings.Contains(unknown.Body.String(), `"code":"not_found"`) {
		t.Fatalf("status = %d body = %s", unknown.Code, unknown.Body.String())
	}
}

func TestCreateRequestBodyLimitFailsClosed(t *testing.T) {
	handler := newTestHandlerWithOptions(t, &fakeService{}, Options{OwnerID: "owner-local", MaxBodyBytes: 32})
	request := httptest.NewRequest(http.MethodPost, "/v1/instances", strings.NewReader(`{"name":"this-body-is-deliberately-longer-than-the-limit","image":"ubuntu"}`))
	request.Header.Set(HeaderIdempotencyKey, "large")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_argument"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func newTestHandler(t *testing.T, service Service) http.Handler {
	t.Helper()
	return newTestHandlerWithOptions(t, service, Options{OwnerID: "owner-local"})
}

func newTestHandlerWithOptions(t *testing.T, service Service, options Options) http.Handler {
	t.Helper()
	handler, err := New(service, options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func assertJSONContains(t *testing.T, body []byte, fragments ...string) {
	t.Helper()
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("invalid JSON %q: %v", body, err)
	}
	compact := new(bytes.Buffer)
	if err := json.Compact(compact, body); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range fragments {
		if !strings.Contains(compact.String(), fragment) {
			t.Errorf("JSON missing %q: %s", fragment, compact.String())
		}
	}
}

type fakeService struct {
	capabilities      runtimeapi.Capabilities
	images            []domain.Image
	instances         []domain.Instance
	operations        []domain.Operation
	created           domain.Instance
	operation         domain.Operation
	extended          domain.Instance
	software          []domain.InstanceSoftware
	installedSoftware domain.InstanceSoftware
	err               error
	execFrames        []execstream.Frame
	execReq           sandbox.ExecRequest
	extendBy          time.Duration
	installPackageID  string

	lastOwner      domain.OwnerID
	lastInstanceID domain.InstanceID
	createInput    instances.CreateInput
	action         Action
	canceled       bool
	eventBatches   [][]domain.OperationEvent
	eventCalls     int
	eventAfter     []int
}

func (f *fakeService) Health(context.Context) error { return f.err }
func (f *fakeService) Capabilities(context.Context) (runtimeapi.Capabilities, error) {
	return f.capabilities, f.err
}
func (f *fakeService) ListImages(_ context.Context, owner domain.OwnerID) ([]domain.Image, error) {
	f.lastOwner = owner
	return f.images, f.err
}
func (f *fakeService) ListInstances(_ context.Context, owner domain.OwnerID) ([]domain.Instance, error) {
	f.lastOwner = owner
	return f.instances, f.err
}
func (f *fakeService) GetInstance(_ context.Context, owner domain.OwnerID, id domain.InstanceID) (domain.Instance, error) {
	f.lastOwner = owner
	f.lastInstanceID = id
	if f.err != nil {
		return domain.Instance{}, f.err
	}
	for _, instance := range f.instances {
		if instance.ID == id {
			return instance, nil
		}
	}
	if f.created.ID != "" && f.created.ID == id {
		return f.created, nil
	}
	return domain.Instance{}, &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
}
func (f *fakeService) SubmitCreate(_ context.Context, input instances.CreateInput) (domain.Instance, domain.Operation, error) {
	f.createInput = input
	return f.created, f.operation, f.err
}
func (f *fakeService) SubmitAction(_ context.Context, owner domain.OwnerID, _ domain.InstanceID, action Action, _ string) (domain.Operation, error) {
	f.lastOwner, f.action = owner, action
	return f.operation, f.err
}
func (f *fakeService) ListOperations(_ context.Context, owner domain.OwnerID) ([]domain.Operation, error) {
	f.lastOwner = owner
	return f.operations, f.err
}
func (f *fakeService) GetOperation(_ context.Context, owner domain.OwnerID, _ domain.OperationID) (domain.Operation, error) {
	f.lastOwner = owner
	return f.operation, f.err
}
func (f *fakeService) ListOperationEventsAfter(_ context.Context, owner domain.OwnerID, _ domain.OperationID, after, _ int) ([]domain.OperationEvent, error) {
	f.lastOwner = owner
	f.eventAfter = append(f.eventAfter, after)
	if f.err != nil {
		return nil, f.err
	}
	if f.eventCalls >= len(f.eventBatches) {
		return nil, nil
	}
	batch := f.eventBatches[f.eventCalls]
	f.eventCalls++
	return batch, nil
}
func (f *fakeService) CancelOperation(_ context.Context, owner domain.OwnerID, _ domain.OperationID) (domain.Operation, error) {
	f.lastOwner = owner
	f.canceled = true
	return f.operation, f.err
}
func (f *fakeService) Exec(_ context.Context, owner domain.OwnerID, id domain.InstanceID, req sandbox.ExecRequest, sink sandbox.FrameSink) error {
	f.lastOwner = owner
	f.lastInstanceID = id
	f.execReq = req
	if f.err != nil {
		return f.err
	}
	for _, frame := range f.execFrames {
		if err := sink.Emit(frame); err != nil {
			return err
		}
	}
	return nil
}
func (f *fakeService) ExtendExpiry(_ context.Context, owner domain.OwnerID, id domain.InstanceID, by time.Duration) (domain.Instance, error) {
	f.lastOwner = owner
	f.lastInstanceID = id
	f.extendBy = by
	if f.err != nil {
		return domain.Instance{}, f.err
	}
	if f.extended.ID != "" {
		return f.extended, nil
	}
	return f.GetInstance(context.Background(), owner, id)
}
func (f *fakeService) ListSoftware(_ context.Context, owner domain.OwnerID, id domain.InstanceID) ([]domain.InstanceSoftware, error) {
	f.lastOwner = owner
	f.lastInstanceID = id
	if f.err != nil {
		return nil, f.err
	}
	return f.software, nil
}
func (f *fakeService) InstallSoftware(_ context.Context, owner domain.OwnerID, id domain.InstanceID, packageID string) (domain.InstanceSoftware, error) {
	f.lastOwner = owner
	f.lastInstanceID = id
	f.installPackageID = packageID
	if f.err != nil {
		return domain.InstanceSoftware{}, f.err
	}
	if f.installedSoftware.PackageID != "" {
		return f.installedSoftware, nil
	}
	return domain.InstanceSoftware{
		InstanceID: id, OwnerID: owner, PackageID: packageID, Status: domain.SoftwareInstalled,
		UpdatedAt: time.Now().UTC(),
	}, nil
}
func (f *fakeService) AttachEgressProfile(_ context.Context, owner domain.OwnerID, id domain.InstanceID, profileID domain.EgressProfileID) (domain.Instance, error) {
	f.lastOwner = owner
	f.lastInstanceID = id
	if f.err != nil {
		return domain.Instance{}, f.err
	}
	instance, err := f.GetInstance(context.Background(), owner, id)
	if err != nil {
		return domain.Instance{}, err
	}
	instance.EgressProfileID = profileID
	return instance, nil
}

var _ Service = (*fakeService)(nil)
var _ = errors.New
