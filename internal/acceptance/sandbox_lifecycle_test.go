// SPDX-License-Identifier: AGPL-3.0-only

package acceptance_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/clock"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/execstream"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
	"github.com/openbox-dev/openbox/internal/sandbox"
	sandboxpool "github.com/openbox-dev/openbox/internal/sandbox/pool"
)

// TestSandboxLifecycleColdCreate covers the Phase 1 no-live-host gate:
// create → ready → exec → extend → expiry → delete cleanup.
func TestSandboxLifecycleColdCreate(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(start)
	service, runtime, store := newAcceptanceService(t, fakeClock, nil)

	created, err := service.Create(ctx, instances.CreateInput{
		OwnerID: "owner-1", Name: "agent", Kind: domain.KindSandbox,
		OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "accept-create",
		Lifetime: 1 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ObservedState != domain.ObservedRunning {
		t.Fatalf("observed=%q", created.ObservedState)
	}
	if created.ExpiresAt == nil || !created.ExpiresAt.Equal(start.Add(time.Hour)) {
		t.Fatalf("expires_at=%v", created.ExpiresAt)
	}

	runtime.SetExecResult(created.RuntimeRef, runtimeapi.ExecResult{ExitCode: 0, Stdout: []byte("ok\n")})
	sink := &frameSink{}
	if err := service.Exec(ctx, created.OwnerID, created.ID, sandbox.ExecRequest{Argv: []string{"true"}}, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.frames) < 2 {
		t.Fatalf("frames=%d", len(sink.frames))
	}
	if _, ok := sink.frames[len(sink.frames)-1].(execstream.ExitFrame); !ok {
		t.Fatalf("last frame=%T", sink.frames[len(sink.frames)-1])
	}

	extended, err := service.ExtendExpiry(ctx, created.OwnerID, created.ID, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	wantExpiry := start.Add(90 * time.Minute)
	if extended.ExpiresAt == nil || !extended.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expires_at=%v want=%v", extended.ExpiresAt, wantExpiry)
	}

	fakeClock.Advance(90 * time.Minute)
	expiry, err := sandbox.NewExpiryScheduler(store, service, sandbox.ExpiryOptions{Clock: fakeClock})
	if err != nil {
		t.Fatal(err)
	}
	marked, err := expiry.RunOnce(ctx)
	if err != nil || marked != 1 {
		t.Fatalf("marked=%d err=%v", marked, err)
	}
	afterExpiry, err := service.GetInstance(ctx, created.OwnerID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterExpiry.DesiredState != domain.DesiredDeleted {
		t.Fatalf("desired=%q", afterExpiry.DesiredState)
	}
	if _, err := runtime.InspectInstance(ctx, created.RuntimeRef); err != nil {
		t.Fatalf("runtime should remain until cleanup: %v", err)
	}

	if err := service.Delete(ctx, created.OwnerID, created.ID, "accept-cleanup"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetInstance(ctx, created.OwnerID, created.ID); err == nil {
		t.Fatal("expected instance gone")
	}
	if _, err := runtime.InspectInstance(ctx, created.RuntimeRef); !errors.Is(err, runtimeapi.ErrNotFound) {
		t.Fatalf("runtime err=%v", err)
	}
}

func TestSandboxLifecycleWarmPool(t *testing.T) {
	ctx := context.Background()
	fakeClock := clock.NewFake(time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC))
	pool := &acceptancePool{}
	service, runtime, _ := newAcceptanceService(t, fakeClock, pool)
	pool.runtime = runtime

	created, err := service.Create(ctx, instances.CreateInput{
		OwnerID: "owner-1", Name: "agent", Kind: domain.KindSandbox,
		OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "accept-warm",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ObservedState != domain.ObservedRunning {
		t.Fatalf("observed=%q", created.ObservedState)
	}
	if pool.claims != 1 || pool.assigns != 1 {
		t.Fatalf("claims=%d assigns=%d", pool.claims, pool.assigns)
	}
}

func TestSandboxLifecycleContainerOnlyHost(t *testing.T) {
	ctx := context.Background()
	fakeClock := clock.NewFake(time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC))
	runtime := fake.New(runtimeapi.Capabilities{
		Architecture: "x86_64", Containers: true, VirtualMachines: false, KVM: false,
		VMAvailability: runtimeapi.VMUnavailableKVMAbsent,
	})
	runtime.AddImage(sandboxImage())
	store := openStore(t)
	service, err := instances.New(runtime, store, instances.Options{
		Now:           fakeClock.Now,
		NewID:         sequentialIDs(),
		NetworkPolicy: nopPolicy{},
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := service.Create(ctx, instances.CreateInput{
		OwnerID: "owner-1", Name: "agent", Kind: domain.KindSandbox,
		OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "accept-container-only",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.RequestedIsolation != domain.IsolationContainerReq || created.ActualIsolation != domain.IsolationContainer {
		t.Fatalf("isolation requested=%q actual=%q", created.RequestedIsolation, created.ActualIsolation)
	}

	_, err = service.Create(ctx, instances.CreateInput{
		OwnerID: "owner-1", Name: "strong-fail", Kind: domain.KindSandbox,
		RequestedIsolation: domain.IsolationStrong,
		OwnerPublicKey:     "ssh-ed25519 owner", IdempotencyKey: "accept-strong-fail",
	})
	var capability *instances.CapabilityError
	if !errors.As(err, &capability) {
		t.Fatalf("err=%v want capability_unavailable", err)
	}
}

func TestSandboxDeleteSurvivesRestartRecovery(t *testing.T) {
	ctx := context.Background()
	fakeClock := clock.NewFake(time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC))
	service, runtime, store := newAcceptanceService(t, fakeClock, nil)
	created, err := service.Create(ctx, instances.CreateInput{
		OwnerID: "owner-1", Name: "agent", Kind: domain.KindSandbox,
		OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "accept-restart",
	})
	if err != nil {
		t.Fatal(err)
	}
	op, err := service.SubmitAction(ctx, created.OwnerID, created.ID, instances.MutationDelete, "accept-delete")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetOperation(ctx, created.OwnerID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverOperation(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetInstance(ctx, created.OwnerID, created.ID); err == nil {
		t.Fatal("expected removed after recovery")
	}
	if _, err := runtime.InspectInstance(ctx, created.RuntimeRef); !errors.Is(err, runtimeapi.ErrNotFound) {
		t.Fatalf("runtime err=%v", err)
	}
}

type frameSink struct{ frames []execstream.Frame }

func (s *frameSink) Emit(frame execstream.Frame) error {
	s.frames = append(s.frames, frame)
	return nil
}

type nopPolicy struct{}

func (nopPolicy) ApplyNetworkPolicy(context.Context, domain.Instance) error  { return nil }
func (nopPolicy) RemoveNetworkPolicy(context.Context, domain.Instance) error { return nil }
func (nopPolicy) NetworkPolicyStatus(domain.Instance) domain.NetworkPolicyStatus {
	return domain.NetworkPolicyStatus{
		EgressMode: domain.EgressRestricted,
		ACLs:       []string{},
		Resolution: domain.AllowlistResolution{State: "idle", Pending: []string{}, Resolved: []string{}, Failed: []string{}},
	}
}

type acceptancePool struct {
	runtime *fake.Runtime
	claims  int
	assigns int
}

func (p *acceptancePool) Enabled() bool                    { return true }
func (p *acceptancePool) Substrate() sandboxpool.Substrate { return sandboxpool.SubstrateContainer }
func (p *acceptancePool) Claim(context.Context) (sandboxpool.Claim, error) {
	p.claims++
	return sandboxpool.Claim{Ref: "slot-1", Running: true}, nil
}
func (p *acceptancePool) Assign(ctx context.Context, req sandboxpool.AssignRequest) error {
	p.assigns++
	_, err := p.runtime.CreateInstance(ctx, runtimeapi.CreateRequest{
		Ref: req.TargetRef, Image: "sha256:sandbox", OwnerPublicKey: req.OwnerPublicKey,
		Unprivileged: true, Metadata: req.Metadata,
		Resources: runtimeapi.Resources{VCPUs: 2, MemoryBytes: 2 << 30, DiskBytes: 10 << 30},
	})
	if err != nil {
		return err
	}
	return p.runtime.StartInstance(ctx, req.TargetRef)
}
func (p *acceptancePool) Discard(context.Context, string) {}

func sandboxImage() runtimeapi.Image {
	return runtimeapi.Image{
		Fingerprint: "sha256:sandbox", Aliases: []string{"openbox:sandbox/ubuntu/24.04"},
		Architecture: "x86_64", Type: "container", CloudInit: true,
	}
}

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), t.TempDir()+"/openbox.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	if err := store.CreateOwner(context.Background(), domain.Owner{ID: "owner-1", Name: "Owner", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return store
}

func sequentialIDs() func() string {
	n := 0
	return func() string {
		n++
		return "id-" + strconv.Itoa(n)
	}
}

func newAcceptanceService(t *testing.T, fakeClock *clock.Fake, pool instances.SandboxPool) (*instances.Service, *fake.Runtime, *sqlite.Store) {
	t.Helper()
	runtime := fake.New(runtimeapi.Capabilities{Architecture: "x86_64", Containers: true})
	runtime.AddImage(sandboxImage())
	store := openStore(t)
	service, err := instances.New(runtime, store, instances.Options{
		Now:           fakeClock.Now,
		NewID:         sequentialIDs(),
		NetworkPolicy: nopPolicy{},
		SandboxPool:   pool,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, runtime, store
}
