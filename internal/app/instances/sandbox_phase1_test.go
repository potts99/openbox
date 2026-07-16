// SPDX-License-Identifier: AGPL-3.0-only

package instances

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/execstream"
	"github.com/openbox-dev/openbox/internal/persistence/sqlite"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
	"github.com/openbox-dev/openbox/internal/sandbox"
	sandboxpool "github.com/openbox-dev/openbox/internal/sandbox/pool"
)

func TestSandboxCreateCustomLifetime(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(sandboxImage())
	service, _, _ := newTestServiceWithIDs(t, runtime, newIDs("instance-1", "operation-1"), runtime)
	created, err := service.Create(context.Background(), CreateInput{
		OwnerID: "owner-1", Name: "agent-box", Kind: domain.KindSandbox,
		OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "sandbox-lifetime",
		Lifetime: 2 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := created.CreatedAt.Add(2 * time.Hour)
	if created.ExpiresAt == nil || !created.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at=%v want=%v", created.ExpiresAt, want)
	}
}

func TestSandboxCreateRejectsLifetimeOverMax(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(sandboxImage())
	service, _, _ := newTestServiceWithIDs(t, runtime, newIDs("instance-1", "operation-1"), runtime)
	_, err := service.Create(context.Background(), CreateInput{
		OwnerID: "owner-1", Name: "agent-box", Kind: domain.KindSandbox,
		OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "sandbox-lifetime-max",
		Lifetime: domain.MaxSandboxLifetime + time.Second,
	})
	assertDomainCode(t, err, domain.CodeInvalidArgument)
}

func TestServiceExecStreamsFrames(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(sandboxImage())
	service, _, _ := newTestServiceWithIDs(t, runtime, newIDs("instance-1", "operation-1"), runtime)
	ctx := context.Background()
	created, err := service.Create(ctx, sandboxCreateInput("sandbox-exec"))
	if err != nil {
		t.Fatal(err)
	}
	runtime.SetExecResult(created.RuntimeRef, runtimeapi.ExecResult{
		ExitCode: 0, Stdout: []byte("hello\n"), Stderr: []byte("warn\n"),
	})
	sink := &recordingSink{}
	if err := service.Exec(ctx, created.OwnerID, created.ID, sandbox.ExecRequest{Argv: []string{"echo", "hello"}}, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.frames) != 3 {
		t.Fatalf("frames=%d want 3", len(sink.frames))
	}
	out, ok := sink.frames[0].(execstream.StdoutFrame)
	if !ok || string(out.Data) != "hello\n" {
		t.Fatalf("stdout=%#v", sink.frames[0])
	}
	errOut, ok := sink.frames[1].(execstream.StderrFrame)
	if !ok || string(errOut.Data) != "warn\n" {
		t.Fatalf("stderr=%#v", sink.frames[1])
	}
	exit, ok := sink.frames[2].(execstream.ExitFrame)
	if !ok || exit.Code != 0 {
		t.Fatalf("exit=%#v", sink.frames[2])
	}
}

func TestServiceExecRejectsNonRunning(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(sandboxImage())
	service, _, _ := newTestServiceWithIDs(t, runtime, newIDs("instance-1", "operation-1", "operation-2"), runtime)
	ctx := context.Background()
	created, err := service.Create(ctx, sandboxCreateInput("sandbox-stopped-exec"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Stop(ctx, created.OwnerID, created.ID, "stop-key"); err != nil {
		t.Fatal(err)
	}
	err = service.Exec(ctx, created.OwnerID, created.ID, sandbox.ExecRequest{Argv: []string{"true"}}, &recordingSink{})
	assertDomainCode(t, err, domain.CodeInvalidTransition)
}

func TestServiceExecRejectsDeleting(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(sandboxImage())
	service, _, _ := newTestServiceWithIDs(t, runtime, newIDs("instance-1", "operation-1", "operation-2"), runtime)
	ctx := context.Background()
	created, err := service.Create(ctx, sandboxCreateInput("sandbox-deleting-exec"))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MarkExpired(ctx, created.OwnerID, created.ID); err != nil {
		t.Fatal(err)
	}
	err = service.Exec(ctx, created.OwnerID, created.ID, sandbox.ExecRequest{Argv: []string{"true"}}, &recordingSink{})
	assertDomainCode(t, err, domain.CodeInvalidTransition)
}

func TestServiceExecBusyGate(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(sandboxImage())
	service, _, _ := newTestServiceWithIDs(t, runtime, newIDs("instance-1", "operation-1"), runtime)
	ctx := context.Background()
	created, err := service.Create(ctx, sandboxCreateInput("sandbox-busy"))
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	runtime.SetExecHook(func(context.Context, runtimeapi.ExecRequest) error {
		<-block
		return nil
	})
	runtime.SetExecResult(created.RuntimeRef, runtimeapi.ExecResult{ExitCode: 0})

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- service.Exec(ctx, created.OwnerID, created.ID, sandbox.ExecRequest{Argv: []string{"sleep"}}, &recordingSink{})
		}()
	}
	// Allow the first two to acquire slots, then expect the third to fail busy.
	time.Sleep(50 * time.Millisecond)
	var busy int
	select {
	case err := <-errs:
		assertDomainCode(t, err, domain.CodeBusy)
		busy++
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for busy rejection")
	}
	close(block)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err == nil {
			continue
		}
		var domainErr *domain.Error
		if errors.As(err, &domainErr) && domainErr.Code == domain.CodeBusy {
			busy++
			continue
		}
		t.Fatalf("unexpected exec error: %v", err)
	}
	if busy < 1 {
		t.Fatal("expected at least one busy rejection")
	}
}

func TestSandboxWarmPoolCreate(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(sandboxImage())
	pool := &fakeSandboxPool{enabled: true, substrate: sandboxpool.SubstrateContainer, runtime: runtime}
	service, _, store := newTestServiceWithPool(t, runtime, pool)
	created, err := service.Create(context.Background(), sandboxCreateInput("warm-pool"))
	if err != nil {
		t.Fatal(err)
	}
	if created.ObservedState != domain.ObservedRunning {
		t.Fatalf("observed=%q", created.ObservedState)
	}
	if pool.claims != 1 || pool.assigns != 1 {
		t.Fatalf("claims=%d assigns=%d", pool.claims, pool.assigns)
	}
	op, found, err := store.GetOperationByIdempotency(context.Background(), created.OwnerID, "warm-pool")
	if err != nil || !found {
		t.Fatalf("operation found=%v err=%v", found, err)
	}
	if op.Status != domain.OperationSucceeded {
		t.Fatalf("operation=%+v", op)
	}
	// Pool Assign creates the personalized runtime instance; the cold create path is skipped.
	if len(runtime.CreateRequests()) != 1 {
		t.Fatalf("create requests=%d want 1 from pool Assign", len(runtime.CreateRequests()))
	}
}

func TestSandboxColdCreateOnPoolMiss(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(sandboxImage())
	pool := &fakeSandboxPool{enabled: true, substrate: sandboxpool.SubstrateContainer, miss: true, runtime: runtime}
	service, _, _ := newTestServiceWithPool(t, runtime, pool)
	created, err := service.Create(context.Background(), sandboxCreateInput("pool-miss"))
	if err != nil {
		t.Fatal(err)
	}
	if created.ObservedState != domain.ObservedRunning {
		t.Fatalf("observed=%q", created.ObservedState)
	}
	if pool.claims != 1 || pool.assigns != 0 {
		t.Fatalf("claims=%d assigns=%d", pool.claims, pool.assigns)
	}
	if len(runtime.CreateRequests()) != 1 {
		t.Fatalf("cold create requests=%d", len(runtime.CreateRequests()))
	}
}

func TestSandboxPoolAssignFailureDiscards(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(sandboxImage())
	pool := &fakeSandboxPool{
		enabled: true, substrate: sandboxpool.SubstrateContainer, runtime: runtime,
		assignErr: errors.New("assign failed"),
	}
	service, _, _ := newTestServiceWithPool(t, runtime, pool)
	_, err := service.Create(context.Background(), sandboxCreateInput("pool-assign-fail"))
	if err == nil {
		t.Fatal("expected assign failure")
	}
	if pool.discards != 1 {
		t.Fatalf("discards=%d", pool.discards)
	}
}

func TestExpiryCleanupAfterWorkerRecovery(t *testing.T) {
	runtime := fake.New(testCapabilities())
	runtime.AddImage(sandboxImage())
	service, _, store := newTestServiceWithIDs(t, runtime, newIDs("instance-1", "operation-1", "operation-2", "operation-3"), runtime)
	ctx := context.Background()
	created, err := service.Create(ctx, sandboxCreateInput("expiry-recovery"))
	if err != nil {
		t.Fatal(err)
	}
	operation, err := service.SubmitAction(ctx, created.OwnerID, created.ID, MutationDelete, "pending-delete")
	if err != nil {
		t.Fatal(err)
	}
	marked, err := service.GetInstance(ctx, created.OwnerID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if marked.DesiredState != domain.DesiredDeleted {
		t.Fatalf("desired=%q", marked.DesiredState)
	}
	// Simulate daemon restart: recover the pending delete operation.
	op, err := store.GetOperation(ctx, created.OwnerID, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetInstance(ctx, created.OwnerID, created.ID); err == nil {
		t.Fatal("expected instance removed after recovered delete")
	}
	if _, err := runtime.InspectInstance(ctx, created.RuntimeRef); !errors.Is(err, runtimeapi.ErrNotFound) {
		t.Fatalf("runtime err=%v", err)
	}
}

func sandboxImage() runtimeapi.Image {
	return runtimeapi.Image{
		Fingerprint:  "sha256:sandbox",
		Aliases:      []string{"openbox:sandbox/ubuntu/24.04"},
		Architecture: "x86_64",
		Type:         "container",
		CloudInit:    true,
	}
}

func sandboxCreateInput(key string) CreateInput {
	return CreateInput{
		OwnerID: "owner-1", Name: "agent-box", Kind: domain.KindSandbox,
		OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: key,
	}
}

type recordingSink struct {
	frames []execstream.Frame
}

func (s *recordingSink) Emit(frame execstream.Frame) error {
	s.frames = append(s.frames, frame)
	return nil
}

type fakeSandboxPool struct {
	enabled   bool
	miss      bool
	substrate sandboxpool.Substrate
	runtime   *fake.Runtime
	assignErr error
	claims    int
	assigns   int
	discards  int
}

func (p *fakeSandboxPool) Enabled() bool                 { return p.enabled }
func (p *fakeSandboxPool) Substrate() sandboxpool.Substrate { return p.substrate }

func (p *fakeSandboxPool) Claim(context.Context) (sandboxpool.Claim, error) {
	p.claims++
	if p.miss {
		return sandboxpool.Claim{}, sandboxpool.ErrMiss
	}
	return sandboxpool.Claim{Ref: "pool-slot-1", Running: true}, nil
}

func (p *fakeSandboxPool) Assign(ctx context.Context, req sandboxpool.AssignRequest) error {
	p.assigns++
	if p.assignErr != nil {
		return p.assignErr
	}
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

func (p *fakeSandboxPool) Discard(context.Context, string) { p.discards++ }

func newTestServiceWithPool(t *testing.T, runtime *fake.Runtime, pool SandboxPool) (*Service, *fake.Runtime, *sqlite.Store) {
	t.Helper()
	service, base, store := newTestServiceWithIDs(t, runtime, newIDs("instance-1", "operation-1", "operation-2", "operation-3", "operation-4"), runtime)
	service.sandboxPool = pool
	return service, base, store
}
