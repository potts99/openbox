// SPDX-License-Identifier: AGPL-3.0-only

package pool_test

import (
	"context"
	"testing"

	"github.com/openbox-dev/openbox/internal/runtime/fake"
	"github.com/openbox-dev/openbox/internal/sandbox/pool"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestClaimPrefersRunningSlot(t *testing.T) {
	t.Parallel()
	r := fake.New(runtimeapi.Capabilities{Architecture: "x86_64", Containers: true, StorageDrivers: []string{"zfs"}})
	r.AddImage(runtimeapi.Image{Fingerprint: "sha256:sandbox", Aliases: []string{"openbox:sandbox/ubuntu/24.04"}, Architecture: "x86_64", Type: "container", CloudInit: true})
	manager := newTestManager(t, r)
	seedGolden(t, r)
	seedPool(t, r, "obx-pool-stopped", pool.StateStopped)
	seedPool(t, r, "obx-pool-running", pool.StateRunning)
	if err := r.StartInstance(context.Background(), "obx-pool-running"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	claim, err := manager.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if claim.Ref != "obx-pool-running" || !claim.Running {
		t.Fatalf("claim = %+v, want running slot", claim)
	}
}

func TestReplenishCreatesStoppedSlots(t *testing.T) {
	t.Parallel()
	r := fake.New(runtimeapi.Capabilities{Architecture: "x86_64", Containers: true, StorageDrivers: []string{"zfs"}})
	r.AddImage(runtimeapi.Image{Fingerprint: "sha256:sandbox", Aliases: []string{"openbox:sandbox/ubuntu/24.04"}, Architecture: "x86_64", Type: "container", CloudInit: true})
	manager := newTestManager(t, r)
	seedGolden(t, r)
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	manager.Replenish(context.Background())
	stats, err := manager.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Stopped < 1 {
		t.Fatalf("stopped = %d, want at least 1", stats.Stopped)
	}
}

func TestAssignRenamesAndStartsSlot(t *testing.T) {
	t.Parallel()
	r := fake.New(runtimeapi.Capabilities{Architecture: "x86_64", Containers: true, StorageDrivers: []string{"zfs"}})
	r.AddImage(runtimeapi.Image{Fingerprint: "sha256:sandbox", Aliases: []string{"openbox:sandbox/ubuntu/24.04"}, Architecture: "x86_64", Type: "container", CloudInit: true})
	manager := newTestManager(t, r)
	seedGolden(t, r)
	seedPool(t, r, "obx-pool-slot", pool.StateStopped)
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	claim, err := manager.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	const ownerKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHRlc3Q="
	if err := manager.Assign(context.Background(), pool.AssignRequest{
		SlotRef: claim.Ref, TargetRef: "obx-user-instance",
		OwnerPublicKey: ownerKey,
		Metadata: map[string]string{
			"user.openbox.managed": "true", "user.openbox.resource": "instance",
			"user.openbox.instance_id": "instance-1", "user.openbox.owner_id": "owner-1",
		},
		WasRunning: claim.Running,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.InspectInstance(context.Background(), "obx-user-instance"); err != nil {
		t.Fatalf("assigned instance missing: %v", err)
	}
	if _, err := r.InspectInstance(context.Background(), claim.Ref); err == nil {
		t.Fatal("pool ref still present after rename")
	}
	got, ok := r.WrittenFile("obx-user-instance", "/root/.ssh/authorized_keys")
	if !ok {
		t.Fatal("owner authorized_keys were not written for stopped pool slot")
	}
	if got != ownerKey+"\n" {
		t.Fatalf("authorized_keys = %q, want %q", got, ownerKey+"\n")
	}
}

func TestAssignWritesOwnerKeysForRunningSlot(t *testing.T) {
	t.Parallel()
	r := fake.New(runtimeapi.Capabilities{Architecture: "x86_64", Containers: true, StorageDrivers: []string{"zfs"}})
	r.AddImage(runtimeapi.Image{Fingerprint: "sha256:sandbox", Aliases: []string{"openbox:sandbox/ubuntu/24.04"}, Architecture: "x86_64", Type: "container", CloudInit: true})
	cfg := pool.DefaultConfig()
	cfg.StoppedTarget = 0
	cfg.RunningTarget = 1
	manager, err := pool.New(r, pool.Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	manager.SetImageForTest("sha256:sandbox")
	seedGolden(t, r)
	seedPool(t, r, "obx-pool-running", pool.StateRunning)
	if err := r.StartInstance(context.Background(), "obx-pool-running"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	claim, err := manager.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !claim.Running {
		t.Fatalf("claim.Running = false, want true")
	}
	const ownerKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHVubmluZw=="
	if err := manager.Assign(context.Background(), pool.AssignRequest{
		SlotRef: claim.Ref, TargetRef: "obx-user-running",
		OwnerPublicKey: ownerKey,
		Metadata: map[string]string{
			"user.openbox.managed": "true", "user.openbox.resource": "instance",
			"user.openbox.instance_id": "instance-2", "user.openbox.owner_id": "owner-1",
		},
		WasRunning: true,
	}); err != nil {
		t.Fatal(err)
	}
	got, ok := r.WrittenFile("obx-user-running", "/root/.ssh/authorized_keys")
	if !ok {
		t.Fatal("owner authorized_keys were not written for running pool slot")
	}
	if got != ownerKey+"\n" {
		t.Fatalf("authorized_keys = %q, want %q", got, ownerKey+"\n")
	}
}

func TestBootstrapSelectsVMSubstrateOnKVMAndZFS(t *testing.T) {
	t.Parallel()
	r := fake.New(runtimeapi.Capabilities{
		Architecture: "x86_64", Containers: true, KVM: true, VirtualMachines: true,
		VMAvailability: runtimeapi.VMAvailable, StorageDrivers: []string{"zfs"},
	})
	r.AddImage(runtimeapi.Image{
		Fingerprint: "sha256:sandbox-vm", Aliases: []string{"openbox:sandbox/ubuntu/24.04"},
		Architecture: "x86_64", Type: "virtual-machine", CloudInit: true,
	})
	manager, err := pool.New(r, pool.Options{Config: pool.DefaultConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := manager.Substrate(); got != pool.SubstrateVM {
		t.Fatalf("substrate = %q, want %q", got, pool.SubstrateVM)
	}
	golden, err := r.InspectInstance(context.Background(), pool.GoldenRef)
	if err != nil {
		t.Fatal(err)
	}
	if !golden.IsVM {
		t.Fatal("golden template is not a VM")
	}
	stats, err := manager.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Substrate != pool.SubstrateVM || !stats.GoldenReady {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestBootstrapFallsBackToContainerWithoutKVM(t *testing.T) {
	t.Parallel()
	r := fake.New(runtimeapi.Capabilities{
		Architecture: "x86_64", Containers: true, StorageDrivers: []string{"zfs"},
	})
	r.AddImage(runtimeapi.Image{
		Fingerprint: "sha256:sandbox", Aliases: []string{"openbox:sandbox/ubuntu/24.04"},
		Architecture: "x86_64", Type: "container", CloudInit: true,
	})
	manager, err := pool.New(r, pool.Options{Config: pool.DefaultConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := manager.Substrate(); got != pool.SubstrateContainer {
		t.Fatalf("substrate = %q, want %q", got, pool.SubstrateContainer)
	}
}

func TestBootstrapDisablesPoolWithoutZFS(t *testing.T) {
	t.Parallel()
	r := fake.New(runtimeapi.Capabilities{
		Architecture: "x86_64", Containers: true, KVM: true, VirtualMachines: true,
		VMAvailability: runtimeapi.VMAvailable, StorageDrivers: []string{"dir"},
	})
	manager, err := pool.New(r, pool.Options{Config: pool.DefaultConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.Enabled() {
		t.Fatal("pool should be disabled without ZFS")
	}
}

func newTestManager(t *testing.T, r *fake.Runtime) *pool.Manager {
	t.Helper()
	cfg := pool.DefaultConfig()
	cfg.StoppedTarget = 1
	cfg.RunningTarget = 0
	manager, err := pool.New(r, pool.Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	manager.SetImageForTest("sha256:sandbox")
	manager.SetSubstrateForTest(pool.SubstrateContainer)
	return manager
}

func seedGolden(t *testing.T, r *fake.Runtime) {
	t.Helper()
	_, err := r.CreatePoolContainer(context.Background(), pool.PoolCreateRequest{
		Ref: pool.GoldenRef, Image: "sha256:sandbox",
		Metadata: map[string]string{pool.RoleLabel: pool.RoleGolden},
	})
	if err != nil && err != runtimeapi.ErrAlreadyExists {
		t.Fatal(err)
	}
	if err := r.CreateSnapshot(context.Background(), pool.GoldenRef, pool.GoldenSnapshot); err != nil && err != runtimeapi.ErrAlreadyExists {
		t.Fatal(err)
	}
}

func seedPool(t *testing.T, r *fake.Runtime, ref, state string) {
	t.Helper()
	if _, err := r.CopyInstance(context.Background(), runtimeapi.CopyRequest{
		SourceRef: pool.GoldenRef, Snapshot: pool.GoldenSnapshot, TargetRef: ref,
		Metadata: map[string]string{
			pool.RoleLabel: pool.RoleSlot, pool.StateLabel: state,
			"user.openbox.managed": "true", "user.openbox.resource": "pool",
		},
	}); err != nil {
		t.Fatal(err)
	}
}
