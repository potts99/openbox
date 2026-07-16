// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"os"
	"testing"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// Optional live-Incus smoke for Phase 2 CoW copy. Never required for the
// default merge gate — set OPENBOX_INCUS_TEST_* to enable.
func TestLiveCheckpointCopySmoke(t *testing.T) {
	socket := os.Getenv("OPENBOX_INCUS_TEST_SOCKET")
	pool := os.Getenv("OPENBOX_INCUS_TEST_STORAGE")
	image := os.Getenv("OPENBOX_INCUS_TEST_IMAGE")
	if socket == "" || pool == "" || image == "" {
		t.Skip("set OPENBOX_INCUS_TEST_SOCKET, OPENBOX_INCUS_TEST_STORAGE, and OPENBOX_INCUS_TEST_IMAGE for live checkpoint smoke")
	}
	adapter, err := New(Options{SocketPath: socket, Project: "openbox", StoragePool: pool})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	caps, err := adapter.DiscoverCapabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sourceRef := "obx-smoke-checkpoint-src"
	targetRef := "obx-smoke-checkpoint-dst"
	t.Cleanup(func() {
		_ = adapter.DeleteInstance(context.Background(), sourceRef)
		_ = adapter.DeleteInstance(context.Background(), targetRef)
	})
	_ = adapter.DeleteInstance(ctx, sourceRef)
	_ = adapter.DeleteInstance(ctx, targetRef)

	if _, err := adapter.CreateInstance(ctx, runtimeapi.CreateRequest{
		Ref: sourceRef, Image: image, Unprivileged: true,
		OwnerPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOpenBoxCheckpointSmoke",
		Metadata: map[string]string{
			ManagedLabel: "true", ResourceLabel: "instance",
			InstanceIDLabel: "smoke-src", OwnerIDLabel: "smoke-owner",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.CreateSnapshot(ctx, sourceRef, "ready"); err != nil {
		t.Fatal(err)
	}
	copied, err := adapter.CopyInstance(ctx, runtimeapi.CopyRequest{
		SourceRef: sourceRef, Snapshot: "ready", TargetRef: targetRef,
		Metadata: map[string]string{
			ManagedLabel: "true", ResourceLabel: "instance",
			InstanceIDLabel: "smoke-dst", OwnerIDLabel: "smoke-owner",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if copied.Ref != targetRef {
		t.Fatalf("copied=%+v", copied)
	}
	t.Logf("storage drivers=%v (CoW claim is product-level; smoke only proves disk snapshot copy)", caps.StorageDrivers)
}
