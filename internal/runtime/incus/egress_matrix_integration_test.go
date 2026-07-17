// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/networkpolicy"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// TestLiveEgressAllowlistMatrix is the opt-in live Incus connectivity matrix
// for Phase 3. Enable with OPENBOX_INCUS_TEST_SOCKET, OPENBOX_INCUS_TEST_STORAGE,
// and OPENBOX_INCUS_TEST_IMAGE.
//
// Probes (from a restricted guest):
//   - DNS to bridge gateway works
//   - HTTPS to an allowlisted public destination succeeds
//   - HTTPS to a non-allowlisted public destination fails
//   - Private/peer CIDR destinations are not reachable
//   - Policy update reprograms the restricted ACL and changes reachability
func TestLiveEgressAllowlistMatrix(t *testing.T) {
	socket := os.Getenv("OPENBOX_INCUS_TEST_SOCKET")
	pool := os.Getenv("OPENBOX_INCUS_TEST_STORAGE")
	image := os.Getenv("OPENBOX_INCUS_TEST_IMAGE")
	if socket == "" || pool == "" || image == "" {
		t.Skip("set OPENBOX_INCUS_TEST_SOCKET, OPENBOX_INCUS_TEST_STORAGE, and OPENBOX_INCUS_TEST_IMAGE to run live egress matrix")
	}

	stamp := time.Now().UTC().Format("20060102150405")
	config := (BootstrapConfig{
		Project: "openbox-egress-" + stamp, Network: "obe-" + stamp, StoragePool: pool,
		ContainerProfile: "obec-" + stamp, VMProfile: "obev-" + stamp,
	}).defaults()
	bootstrap, err := New(Options{SocketPath: socket, Timeout: 90 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := bootstrap.Bootstrap(ctx, config); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupIntegrationResources(bootstrap, config) })

	adapter, err := New(Options{
		SocketPath: socket, Timeout: 90 * time.Second, Project: config.Project,
		ContainerProfile: config.ContainerProfile, StoragePool: pool,
	})
	if err != nil {
		t.Fatal(err)
	}

	ref := "obx-egress-" + stamp
	instanceID := domain.InstanceID("egress-matrix-" + stamp)
	instance := domain.Instance{
		ID: instanceID, Kind: domain.KindSandbox, RuntimeRef: ref,
		EgressMode: domain.EgressRestricted, EgressProfileID: domain.EgressProfileIDRestricted,
	}
	allowIP := "1.1.1.1"
	denyHost := "example.com"

	t.Cleanup(func() {
		_ = adapter.StopInstance(context.Background(), ref)
		_ = adapter.DeleteInstance(context.Background(), ref)
		_ = adapter.RemoveNetworkPolicy(context.Background(), instance)
	})
	_ = adapter.DeleteInstance(ctx, ref)

	if _, err := adapter.CreateInstance(ctx, runtimeapi.CreateRequest{
		Ref: ref, Image: image, Unprivileged: true,
		OwnerPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOpenBoxEgressMatrix",
		Metadata: map[string]string{
			ManagedLabel: "true", ResourceLabel: "instance",
			InstanceIDLabel: string(instanceID), OwnerIDLabel: "egress-matrix-owner",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.StartInstance(ctx, ref); err != nil {
		t.Fatal(err)
	}
	waitGuestReady(t, adapter, ref)

	if err := adapter.ProgramNetworkPolicy(ctx, PolicyApply{
		Instance: instance, Mode: domain.EgressRestricted, Destinations: []string{allowIP},
		Resolution: domain.AllowlistResolution{State: "idle", Pending: []string{}, Resolved: []string{}, Failed: []string{}},
	}); err != nil {
		t.Fatalf("program restricted policy: %v", err)
	}
	if err := adapter.VerifyNetworkPolicy(ctx, instance); err != nil {
		t.Fatalf("verify restricted policy: %v", err)
	}
	status := adapter.NetworkPolicyStatus(instance)
	wantACL := networkpolicy.RestrictedACLName(string(instanceID))
	if len(status.ACLs) != 2 || status.ACLs[0] != DefaultDenyACLName || status.ACLs[1] != wantACL {
		t.Fatalf("acls=%v want [%s %s]", status.ACLs, DefaultDenyACLName, wantACL)
	}

	// DNS to bridge gateway must work under default-deny + restricted.
	dnsProbe := fmt.Sprintf(
		`(command -v busybox >/dev/null && busybox nslookup example.com %s) || nslookup example.com %s || (command -v dig >/dev/null && dig @%s +time=3 +tries=1 +short example.com | grep -q .)`,
		ManagedBridgeGatewayHost, ManagedBridgeGatewayHost, ManagedBridgeGatewayHost,
	)
	if code := guestExecCode(t, adapter, ref, []string{"sh", "-c", dnsProbe}); code != 0 {
		t.Fatalf("DNS via bridge gateway %s should succeed; exit=%d", ManagedBridgeGatewayHost, code)
	}

	// Allowlisted public HTTPS should succeed.
	allowProbe := fmt.Sprintf("wget -q -O /dev/null --timeout=8 https://%s/ || curl -fsS --max-time 8 https://%s/ -o /dev/null", allowIP, allowIP)
	if code := guestExecCode(t, adapter, ref, []string{"sh", "-c", allowProbe}); code != 0 {
		t.Fatalf("allowlisted HTTPS to %s should succeed; exit=%d", allowIP, code)
	}

	// Non-allowlisted public HTTPS should fail closed.
	denyProbe := fmt.Sprintf("wget -q -O /dev/null --timeout=5 https://%s/ || curl -fsS --max-time 5 https://%s/ -o /dev/null", denyHost, denyHost)
	if code := guestExecCode(t, adapter, ref, []string{"sh", "-c", denyProbe}); code == 0 {
		t.Fatalf("non-allowlisted HTTPS to %s should fail under restricted policy", denyHost)
	}

	// Private / peer CIDR must remain denied (RFC5737 TEST-NET-1).
	privateProbe := "wget -q -O /dev/null --timeout=3 http://192.0.2.1/ || curl -fsS --max-time 3 http://192.0.2.1/ -o /dev/null"
	if code := guestExecCode(t, adapter, ref, []string{"sh", "-c", privateProbe}); code == 0 {
		t.Fatal("private TEST-NET destination should be denied")
	}

	// Policy update: empty allowlist should revoke previous public allow.
	if err := adapter.ProgramNetworkPolicy(ctx, PolicyApply{
		Instance: instance, Mode: domain.EgressRestricted, Destinations: nil,
		Resolution: domain.AllowlistResolution{State: "idle", Pending: []string{}, Resolved: []string{}, Failed: []string{}},
	}); err != nil {
		t.Fatalf("reprogram empty allowlist: %v", err)
	}
	if code := guestExecCode(t, adapter, ref, []string{"sh", "-c", allowProbe}); code == 0 {
		t.Fatalf("HTTPS to %s should fail after allowlist revocation", allowIP)
	}
}

func waitGuestReady(t *testing.T, adapter *Adapter, ref string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		result, err := adapter.Exec(context.Background(), runtimeapi.ExecRequest{
			Ref: ref, Command: []string{"true"},
		})
		if err == nil && result.ExitCode == 0 {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("guest did not become ready for exec")
}

func guestExecCode(t *testing.T, adapter *Adapter, ref string, command []string) int {
	t.Helper()
	result, err := adapter.Exec(context.Background(), runtimeapi.ExecRequest{
		Ref: ref, Command: command,
	})
	if err != nil {
		t.Logf("exec %v err=%v", command, err)
		return 1
	}
	if result.ExitCode != 0 {
		t.Logf("exec %v exit=%d stdout=%q stderr=%q", command, result.ExitCode,
			strings.TrimSpace(string(result.Stdout)), strings.TrimSpace(string(result.Stderr)))
	}
	return result.ExitCode
}
