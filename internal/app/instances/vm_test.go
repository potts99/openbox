// SPDX-License-Identifier: AGPL-3.0-only

package instances

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openbox-dev/openbox/internal/domain"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
)

func TestIsolationSelectionAndActualPersistence(t *testing.T) {
	tests := []struct {
		name      string
		request   domain.IsolationRequest
		caps      runtimeapi.Capabilities
		want      domain.IsolationType
		wantError bool
	}{
		{name: "standard always container", request: domain.IsolationStandard, caps: usableVMCapabilities(), want: domain.IsolationContainer},
		{name: "strong supported", request: domain.IsolationStrong, caps: usableVMCapabilities(), want: domain.IsolationVM},
		{name: "best supported", request: domain.IsolationBestAvailable, caps: usableVMCapabilities(), want: domain.IsolationVM},
		{name: "best KVM absent", request: domain.IsolationBestAvailable, caps: unavailableVMCapabilities(runtimeapi.VMUnavailableKVMAbsent), want: domain.IsolationContainer},
		{name: "best permission denied", request: domain.IsolationBestAvailable, caps: unavailableVMCapabilities(runtimeapi.VMUnavailableKVMPermission), want: domain.IsolationContainer},
		{name: "best nested unavailable", request: domain.IsolationBestAvailable, caps: unavailableVMCapabilities(runtimeapi.VMUnavailableNestedVirtualization), want: domain.IsolationContainer},
		{name: "strong KVM absent", request: domain.IsolationStrong, caps: unavailableVMCapabilities(runtimeapi.VMUnavailableKVMAbsent), wantError: true},
		{name: "strong permission denied", request: domain.IsolationStrong, caps: unavailableVMCapabilities(runtimeapi.VMUnavailableKVMPermission), wantError: true},
		{name: "strong nested unavailable", request: domain.IsolationStrong, caps: unavailableVMCapabilities(runtimeapi.VMUnavailableNestedVirtualization), wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := fake.New(test.caps)
			addDualImages(r)
			service, _, store := newTestService(t, r)
			input := createInput()
			input.RequestedIsolation = test.request
			created, err := service.Create(context.Background(), input)
			if test.wantError {
				var capabilityErr *CapabilityError
				if !errors.As(err, &capabilityErr) || capabilityErr.Capability != "strong_isolation" {
					t.Fatalf("error = %v", err)
				}
				if _, found, lookupErr := store.GetOperationByIdempotency(context.Background(), input.OwnerID, input.IdempotencyKey); lookupErr != nil || found {
					t.Fatalf("strong failure created operation: found=%v err=%v", found, lookupErr)
				}
				if len(r.CreateRequests()) != 0 {
					t.Fatal("strong failure reached runtime create")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if created.ActualIsolation != test.want {
				t.Fatalf("actual isolation = %s, want %s", created.ActualIsolation, test.want)
			}
			requests := r.CreateRequests()
			if len(requests) != 1 || requests[0].VM != (test.want == domain.IsolationVM) || requests[0].Unprivileged != (test.want == domain.IsolationContainer) {
				t.Fatalf("create requests = %+v", requests)
			}
			wantFingerprint := "sha256:container"
			if test.want == domain.IsolationVM {
				wantFingerprint = "sha256:vm"
			}
			if requests[0].Image != wantFingerprint {
				t.Fatalf("runtime image = %q, want immutable %q", requests[0].Image, wantFingerprint)
			}
		})
	}
}

func TestVMLifecycleSharesApplicationContract(t *testing.T) {
	r := fake.New(usableVMCapabilities())
	addDualImages(r)
	service, _, _ := newTestService(t, r)
	input := createInput()
	input.RequestedIsolation = domain.IsolationStrong
	instance, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Stop(context.Background(), instance.OwnerID, instance.ID, "vm-stop"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(context.Background(), instance.OwnerID, instance.ID, "vm-start"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Restart(context.Background(), instance.OwnerID, instance.ID, "vm-restart"); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), instance.OwnerID, instance.ID, "vm-delete"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.InspectInstance(context.Background(), instance.RuntimeRef); !errors.Is(err, runtimeapi.ErrNotFound) {
		t.Fatalf("VM remains after delete: %v", err)
	}
}

func TestVMReadinessFailureRecordsStageAndCleansOwnedPartialVM(t *testing.T) {
	base := fake.New(usableVMCapabilities())
	addDualImages(base)
	r := &readinessFailureRuntime{Runtime: base, failure: errors.New("agent timeout")}
	service, _, store := newTestService(t, r)
	input := createInput()
	input.RequestedIsolation = domain.IsolationStrong
	_, err := service.Create(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "agent timeout") {
		t.Fatalf("error = %v", err)
	}
	requests := base.CreateRequests()
	if len(requests) != 1 {
		t.Fatalf("create requests = %+v", requests)
	}
	if _, inspectErr := base.InspectInstance(context.Background(), requests[0].Ref); !errors.Is(inspectErr, runtimeapi.ErrNotFound) {
		t.Fatalf("partial VM was not cleaned: %v", inspectErr)
	}
	operation, found, lookupErr := store.GetOperationByIdempotency(context.Background(), input.OwnerID, input.IdempotencyKey)
	if lookupErr != nil || !found || operation.Stage != "waiting_for_agent" || operation.Status != domain.OperationRunning {
		t.Fatalf("operation = %+v found=%v err=%v", operation, found, lookupErr)
	}
}

func TestVMPartialCleanupRefusesReplacementContainer(t *testing.T) {
	base := fake.New(usableVMCapabilities())
	addDualImages(base)
	r := &readinessFailureRuntime{Runtime: base, failure: errors.New("agent timeout"), replace: true}
	service, _, _ := newTestService(t, r)
	input := createInput()
	input.RequestedIsolation = domain.IsolationStrong
	_, err := service.Create(context.Background(), input)
	var conflict *IdentityConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("cleanup error = %v, want identity conflict", err)
	}
	requests := base.CreateRequests()
	replacement, inspectErr := base.InspectInstance(context.Background(), requests[0].Ref)
	if inspectErr != nil || replacement.IsVM || replacement.Metadata[MetadataInstanceID] != "replacement" {
		t.Fatalf("replacement was damaged: %+v err=%v", replacement, inspectErr)
	}
}

func TestVMCreateUncertainOutcomeUsesIdentityCheckedCleanup(t *testing.T) {
	lostResponse := errors.New("lost create response")
	tests := []struct {
		name         string
		replacement  string
		wantExists   bool
		wantConflict bool
	}{
		{name: "matching owned VM is cleaned"},
		{name: "replacement container is preserved", replacement: "replacement", wantExists: true, wantConflict: true},
		{name: "unmanaged container is preserved", replacement: "unmanaged", wantExists: true, wantConflict: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := fake.New(usableVMCapabilities())
			addDualImages(base)
			r := &uncertainCreateRuntime{Runtime: base, failure: lostResponse, replacement: test.replacement}
			service, _, _ := newTestService(t, r)
			input := createInput()
			input.RequestedIsolation = domain.IsolationStrong
			_, err := service.Create(context.Background(), input)
			if !errors.Is(err, lostResponse) {
				t.Fatalf("original create error was lost: %v", err)
			}
			var conflict *IdentityConflictError
			if errors.As(err, &conflict) != test.wantConflict {
				t.Fatalf("identity conflict=%v error=%v", conflict, err)
			}
			requests := base.CreateRequests()
			if len(requests) == 0 {
				t.Fatal("uncertain runtime did not create a resource")
			}
			remaining, inspectErr := base.InspectInstance(context.Background(), requests[0].Ref)
			if test.wantExists {
				if inspectErr != nil || remaining.IsVM {
					t.Fatalf("replacement was not preserved: %+v err=%v", remaining, inspectErr)
				}
			} else if !errors.Is(inspectErr, runtimeapi.ErrNotFound) {
				t.Fatalf("matching partial VM remains: %+v err=%v", remaining, inspectErr)
			}
		})
	}
}

func TestVMRequiresCloudInitCompatibleVMImageBeforeOperation(t *testing.T) {
	r := fake.New(usableVMCapabilities())
	r.AddImage(runtimeapi.Image{Fingerprint: "sha256:vm", Aliases: []string{"ubuntu"}, Architecture: "x86_64", Type: "virtual-machine", CloudInit: false})
	service, _, store := newTestService(t, r)
	input := createInput()
	input.RequestedIsolation = domain.IsolationStrong
	_, err := service.Create(context.Background(), input)
	var capabilityErr *CapabilityError
	if !errors.As(err, &capabilityErr) || capabilityErr.Capability != "image_cloud_init" {
		t.Fatalf("error = %v", err)
	}
	if _, found, lookupErr := store.GetOperationByIdempotency(context.Background(), input.OwnerID, input.IdempotencyKey); lookupErr != nil || found {
		t.Fatalf("incompatible image created operation: found=%v err=%v", found, lookupErr)
	}
}

type readinessFailureRuntime struct {
	*fake.Runtime
	failure error
	replace bool
}

type uncertainCreateRuntime struct {
	*fake.Runtime
	failure     error
	replacement string
}

func (r *uncertainCreateRuntime) CreateInstance(ctx context.Context, request runtimeapi.CreateRequest) (runtimeapi.Instance, error) {
	created, err := r.Runtime.CreateInstance(ctx, request)
	if err != nil {
		return created, err
	}
	if r.replacement != "" {
		_ = r.Runtime.DeleteInstance(ctx, request.Ref)
		metadata := map[string]string(nil)
		if r.replacement == "replacement" {
			metadata = managedMetadata("owner-1", "replacement")
		}
		_, _ = r.Runtime.CreateInstance(ctx, runtimeapi.CreateRequest{
			Ref: request.Ref, Image: "sha256:container", Unprivileged: true, Metadata: metadata,
		})
	}
	return runtimeapi.Instance{}, r.failure
}

func (r *readinessFailureRuntime) WaitInstanceReady(ctx context.Context, request runtimeapi.ReadinessRequest) error {
	if request.Stage != nil {
		if err := request.Stage("waiting_for_agent"); err != nil {
			return err
		}
	}
	if r.replace {
		_ = r.Runtime.DeleteInstance(ctx, request.Ref)
		_, _ = r.Runtime.CreateInstance(ctx, runtimeapi.CreateRequest{
			Ref: request.Ref, Image: "sha256:container", Unprivileged: true,
			Metadata: managedMetadata("owner-1", "replacement"),
		})
	}
	return r.failure
}

func usableVMCapabilities() runtimeapi.Capabilities {
	return runtimeapi.Capabilities{
		Architecture: "x86_64", Containers: true, KVM: true, VirtualMachines: true,
		VMAvailability: runtimeapi.VMAvailable,
	}
}

func unavailableVMCapabilities(status runtimeapi.VMAvailability) runtimeapi.Capabilities {
	return runtimeapi.Capabilities{
		Architecture: "x86_64", Containers: true, VMAvailability: status, VMReason: string(status),
	}
}

func addDualImages(r *fake.Runtime) {
	r.AddImage(runtimeapi.Image{Fingerprint: "sha256:container", Aliases: []string{"ubuntu"}, Architecture: "x86_64", Type: "container", CloudInit: true})
	r.AddImage(runtimeapi.Image{Fingerprint: "sha256:vm", Aliases: []string{"ubuntu"}, Architecture: "x86_64", Type: "virtual-machine", CloudInit: true})
}
