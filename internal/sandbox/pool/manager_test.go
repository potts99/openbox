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
	if err := manager.Assign(context.Background(), pool.AssignRequest{
		SlotRef: claim.Ref, TargetRef: "obx-user-instance",
		OwnerPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHRlc3Q=",
		Metadata: map[string]string{
			"user.openbox.managed": "true", "user.openbox.resource": "instance",
			"user.openbox.instance_id": "instance-1", "user.openbox.owner_id": "owner-1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.InspectInstance(context.Background(), "obx-user-instance"); err != nil {
		t.Fatalf("assigned instance missing: %v", err)
	}
	if _, err := r.InspectInstance(context.Background(), claim.Ref); err == nil {
		t.Fatal("pool ref still present after rename")
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
