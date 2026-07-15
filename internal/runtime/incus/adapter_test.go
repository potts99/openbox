// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

type staticProbe struct {
	capabilities HostCapabilities
}

func (p staticProbe) Discover(ctx context.Context) (HostCapabilities, error) {
	if err := ctx.Err(); err != nil {
		return HostCapabilities{}, err
	}
	return p.capabilities, nil
}

func TestDiscoverCapabilitiesWithoutKVM(t *testing.T) {
	socket := serveUnixHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.0" {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeSync(w, map[string]any{
			"api_extensions": []string{"virtual-machines"},
			"environment": map[string]any{
				"kernel_architecture":       "x86_64",
				"server_version":            "6.23",
				"storage_supported_drivers": []map[string]any{{"name": "dir"}, {"name": "zfs"}},
			},
		})
	}))
	adapter, err := New(Options{
		SocketPath: socket,
		HostProbe: staticProbe{capabilities: HostCapabilities{
			Architecture: "amd64", Cgroups: true, KVM: false,
			Namespaces:   map[string]bool{"mnt": true, "net": true, "pid": true, "user": true},
			NetworkTools: map[string]bool{"dnsmasq": true, "ip": true, "nft": true},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := adapter.DiscoverCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.Containers || capabilities.KVM || capabilities.VirtualMachines {
		t.Fatalf("unexpected isolation capabilities: %#v", capabilities)
	}
	if capabilities.IncusVersion != "6.23" || !reflect.DeepEqual(capabilities.StorageDrivers, []string{"dir", "zfs"}) {
		t.Fatalf("unexpected daemon capabilities: %#v", capabilities)
	}
}

func TestAdapterRejectsRemoteEndpoints(t *testing.T) {
	if _, err := New(Options{SocketPath: "https://incus.example"}); err == nil {
		t.Fatal("remote Incus endpoint was accepted")
	}
	if _, err := New(Options{SocketPath: "relative/incus.socket"}); err == nil {
		t.Fatal("relative Incus socket was accepted")
	}
}

func TestAdapterTimeoutValidation(t *testing.T) {
	adapter, err := New(Options{SocketPath: "/tmp/incus.socket"})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.timeout != 10*time.Second {
		t.Fatalf("zero timeout default = %s, want 10s", adapter.timeout)
	}
	if adapter.operationTimeout != 2*time.Minute {
		t.Fatalf("operation timeout default = %s, want 2m", adapter.operationTimeout)
	}
	if _, err := New(Options{SocketPath: "/tmp/incus.socket", Timeout: -time.Second}); err == nil || !strings.Contains(err.Error(), "must not be negative") {
		t.Fatalf("negative timeout error = %v", err)
	}
	if _, err := New(Options{SocketPath: "/tmp/incus.socket", OperationTimeout: -time.Second}); err == nil || !strings.Contains(err.Error(), "operation timeout") {
		t.Fatalf("negative operation timeout error = %v", err)
	}
}

func TestAsyncOperationUsesSeparateBoundedTimeout(t *testing.T) {
	t.Run("longer than request timeout succeeds", func(t *testing.T) {
		socket := serveUnixHTTP(t, asyncOperationHandler(60*time.Millisecond))
		adapter, err := New(Options{SocketPath: socket, Timeout: 10 * time.Millisecond, OperationTimeout: 250 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		if err := adapter.StartInstance(context.Background(), "instance"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("operation wait remains bounded", func(t *testing.T) {
		socket := serveUnixHTTP(t, asyncOperationHandler(250*time.Millisecond))
		adapter, err := New(Options{SocketPath: socket, Timeout: 10 * time.Millisecond, OperationTimeout: 25 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		err = adapter.StartInstance(context.Background(), "instance")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error=%v", err)
		}
		if elapsed := time.Since(started); elapsed > 150*time.Millisecond {
			t.Fatalf("operation timeout took %s", elapsed)
		}
	})
}

func asyncOperationHandler(delay time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/state") {
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(apiResponse{Type: "async", StatusCode: http.StatusAccepted, Operation: "/1.0/operations/test"})
			return
		}
		if r.URL.Path == "/1.0/operations/test/wait" {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
				writeSync(w, map[string]any{"status_code": 200, "err": ""})
				return
			}
		}
		writeError(w, http.StatusNotFound, "not found")
	})
}

func TestRequestsHonorCancellationAndTimeout(t *testing.T) {
	socket := serveUnixHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(time.Second):
			writeSync(w, map[string]any{})
		}
	}))
	adapter, err := New(Options{SocketPath: socket, Timeout: 25 * time.Millisecond, HostProbe: staticProbe{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.DiscoverCapabilities(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}

	adapter, err = New(Options{SocketPath: socket, Timeout: time.Second, HostProbe: staticProbe{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = adapter.DiscoverCapabilities(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestBootstrapIsIdempotentAndLeavesUnknownResourcesUntouched(t *testing.T) {
	api := newBootstrapAPI()
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket})
	if err != nil {
		t.Fatal(err)
	}
	config := BootstrapConfig{Project: "openbox", Network: "openbox0", StoragePool: "default"}
	if err := adapter.Bootstrap(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	network := api.resources["network/openbox0"]
	network.Config["user.server-added"] = "preserved"
	api.resources["network/openbox0"] = network
	profile := api.resources["profile/openbox-container"]
	profile.Config["user.server-added"] = "preserved"
	profile.Devices["root"]["size.state"] = "server-added"
	profile.Devices["extra"] = map[string]string{"type": "none"}
	api.resources["profile/openbox-container"] = profile
	api.mu.Unlock()
	first := api.snapshot()
	if err := adapter.Bootstrap(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	second := api.snapshot()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("second bootstrap changed configuration:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if api.posts != 6 {
		t.Fatalf("POST count = %d, want 6", api.posts)
	}
	for key, value := range second {
		if key == "project/unrelated" || key == "storage/default" {
			if value.Config[ManagedLabel] != "" {
				t.Fatalf("referenced or unrelated resource was labeled: %#v", value)
			}
			continue
		}
		if value.Config[ManagedLabel] != "true" || value.Config[ResourceLabel] == "" {
			t.Fatalf("managed resource %s lacks ownership labels: %#v", key, value)
		}
	}
	if second["project/openbox"].Config["features.images"] != "false" {
		t.Fatalf("OpenBox project does not inherit default-project images: %#v", second["project/openbox"].Config)
	}
}

func TestBootstrapCreatesACLAndAttachesItToManagedNICs(t *testing.T) {
	api := newBootstrapAPI()
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket})
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Bootstrap(context.Background(), BootstrapConfig{Project: "openbox", Network: "openbox0", StoragePool: "default"}); err != nil {
		t.Fatal(err)
	}

	resources := api.snapshot()
	aclResource, exists := resources["network-acl/openbox-default-deny"]
	if !exists {
		t.Fatal("managed default-deny ACL was not created")
	}
	if _, exists := resources["network-acl/"+StandardEgressACLName]; !exists {
		t.Fatal("managed standard egress ACL was not created")
	}
	if aclResource.Config[ManagedLabel] != "true" || aclResource.Config[ResourceLabel] != "network-acl" {
		t.Fatalf("ACL ownership labels = %#v", aclResource.Config)
	}
	for _, name := range []string{"openbox-container", "openbox-vm"} {
		if got := resources["profile/"+name].Devices["eth0"]["security.acls"]; got != "openbox-default-deny" {
			t.Fatalf("%s eth0 security.acls = %q, want openbox-default-deny", name, got)
		}
	}
	for _, wanted := range []networkACLRule{
		{Action: "allow", Destination: "10.42.0.1", Protocol: "udp", DestinationPort: "53"},
		{Action: "allow", Destination: "10.42.0.1", Protocol: "tcp", DestinationPort: "53"},
		{Action: "allow", Destination: "10.42.0.1", Protocol: "tcp", DestinationPort: "18789"},
	} {
		if !containsNetworkACLRule(aclResource.Egress, wanted) {
			t.Fatalf("ACL egress rules = %#v, missing %#v", aclResource.Egress, wanted)
		}
	}
	if !containsNetworkACLRule(aclResource.Ingress, networkACLRule{Action: "allow", Source: "10.42.0.1", Protocol: "tcp", DestinationPort: "22"}) {
		t.Fatalf("ACL ingress rules = %#v, missing SSH gateway rule", aclResource.Ingress)
	}
}

func TestEgressACLResourcesAndNICComposition(t *testing.T) {
	standard := standardEgressACLResource()
	if standard.Name != StandardEgressACLName {
		t.Fatalf("standard ACL name = %q, want %q", standard.Name, StandardEgressACLName)
	}
	if !containsNetworkACLRule(standard.Egress, networkACLRule{Action: "allow", Destination: "0.0.0.0/0"}) {
		t.Fatalf("standard ACL egress rules = %#v, missing internet allow", standard.Egress)
	}
	if containsNetworkACLRule(standard.Egress, networkACLRule{Action: "allow", Destination: "10.42.0.0/24"}) {
		t.Fatalf("standard ACL egress rules = %#v, must not allow peer CIDR", standard.Egress)
	}

	restricted := RestrictedACL("openbox-egress-restricted-profile-1", []string{"203.0.113.9", "198.51.100.0/24"})
	if restricted.Name != "openbox-egress-restricted-profile-1" {
		t.Fatalf("restricted ACL name = %q", restricted.Name)
	}
	if containsNetworkACLRule(restricted.Egress, networkACLRule{Action: "allow", Destination: "0.0.0.0/0"}) {
		t.Fatalf("restricted ACL egress rules = %#v, must not allow the internet", restricted.Egress)
	}
	for _, destination := range []string{"203.0.113.9", "198.51.100.0/24"} {
		if !containsNetworkACLRule(restricted.Egress, networkACLRule{Action: "allow", Destination: destination}) {
			t.Fatalf("restricted ACL egress rules = %#v, missing %q", restricted.Egress, destination)
		}
	}
	if len(restricted.Egress) != 2 {
		t.Fatalf("restricted ACL egress rules = %#v, want only allowlisted destinations", restricted.Egress)
	}

	if got, want := NICACLs(domain.EgressStandard), []string{DefaultDenyACLName, StandardEgressACLName}; !reflect.DeepEqual(got, want) {
		t.Fatalf("standard NIC ACLs = %#v, want %#v", got, want)
	}
	if got, want := NICACLs(domain.EgressRestricted, restricted.Name), []string{DefaultDenyACLName, restricted.Name}; !reflect.DeepEqual(got, want) {
		t.Fatalf("restricted NIC ACLs = %#v, want %#v", got, want)
	}
}

func TestEnsureRestrictedACLEnsuresNamedAllowlist(t *testing.T) {
	api := newBootstrapAPI()
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket})
	if err != nil {
		t.Fatal(err)
	}

	if err := adapter.EnsureRestrictedACL(context.Background(), "openbox-egress-restricted-profile-1", []string{"203.0.113.9"}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.EnsureRestrictedACL(context.Background(), "openbox-egress-restricted-profile-1", []string{"203.0.113.9"}); err != nil {
		t.Fatal(err)
	}
	if api.posts != 1 {
		t.Fatalf("restricted ACL POST count = %d, want 1", api.posts)
	}
	if _, exists := api.snapshot()["network-acl/openbox-egress-restricted-profile-1"]; !exists {
		t.Fatal("restricted ACL was not created")
	}
}

func TestBootstrapRefusesUnmanagedACLNameConflict(t *testing.T) {
	api := newBootstrapAPI()
	api.resources["project/openbox"] = resource{Name: "openbox", Config: managedConfig("project", map[string]string{
		"features.images": "false", "features.networks": "false", "features.profiles": "true",
	})}
	api.resources["network/openbox0"] = networkResource(BootstrapConfig{Network: "openbox0"})
	api.resources["network-acl/openbox-default-deny"] = resource{
		Name: "openbox-default-deny", Config: map[string]string{"user.owner": "someone-else"},
	}
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket})
	if err != nil {
		t.Fatal(err)
	}

	err = adapter.Bootstrap(context.Background(), BootstrapConfig{Project: "openbox", Network: "openbox0", StoragePool: "default"})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Kind != "network ACL" {
		t.Fatalf("error = %v, want network ACL ConflictError", err)
	}
	if api.posts != 0 {
		t.Fatalf("bootstrap mutated resources before ACL conflict: %d POSTs", api.posts)
	}
}

func TestBootstrapRefusesUnknownNameConflict(t *testing.T) {
	api := newBootstrapAPI()
	api.resources["project/openbox"] = resource{Name: "openbox", Config: map[string]string{"user.note": "belongs elsewhere"}}
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket})
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.Bootstrap(context.Background(), BootstrapConfig{Project: "openbox", StoragePool: "default"})
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want ConflictError", err)
	}
	if api.posts != 0 {
		t.Fatalf("bootstrap mutated resources before conflict: %d POSTs", api.posts)
	}
}

func TestBootstrapRejectsProjectWithoutImageInheritance(t *testing.T) {
	api := newBootstrapAPI()
	api.resources["project/openbox"] = resource{Name: "openbox", Config: managedConfig("project", map[string]string{
		"features.images": "true", "features.networks": "true", "features.profiles": "true",
	})}
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket})
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.Bootstrap(context.Background(), BootstrapConfig{Project: "openbox", StoragePool: "default"})
	var drift *DriftError
	if !errors.As(err, &drift) || !containsField(drift.Fields, "config.features.images") {
		t.Fatalf("error=%v drift=%+v", err, drift)
	}
	if api.posts != 0 {
		t.Fatalf("bootstrap mutated before image-inheritance drift: %d posts", api.posts)
	}
}

func TestBootstrapChecksAllNamesBeforeMutatingManagedProject(t *testing.T) {
	api := newBootstrapAPI()
	api.resources["project/openbox"] = resource{Name: "openbox", Config: managedConfig("project", map[string]string{"features.images": "false", "features.networks": "false", "features.profiles": "true"})}
	api.resources["network/openbox0"] = resource{Name: "openbox0", Config: map[string]string{"user.owner": "someone-else"}}
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket})
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.Bootstrap(context.Background(), BootstrapConfig{Project: "openbox", Network: "openbox0", StoragePool: "default"})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Kind != "network" {
		t.Fatalf("error = %v, want network ConflictError", err)
	}
	if api.posts != 0 {
		t.Fatalf("bootstrap mutated resources before conflict: %d POSTs", api.posts)
	}
}

func TestBootstrapRejectsLabelledConfigurationDriftBeforeMutation(t *testing.T) {
	api := newBootstrapAPI()
	api.resources["project/openbox"] = resource{Name: "openbox", Config: managedConfig("project", map[string]string{"features.images": "false", "features.networks": "false", "features.profiles": "true"})}
	driftedNetwork := networkResource(BootstrapConfig{Network: "openbox0"})
	driftedNetwork.Config["ipv4.nat"] = "false"
	api.resources["network/openbox0"] = driftedNetwork
	api.resources["profile/openbox-container"] = profileResource("openbox-container", "container-profile", BootstrapConfig{Network: "openbox0", StoragePool: "default"})
	api.resources["profile/openbox-vm"] = profileResource("openbox-vm", "vm-profile", BootstrapConfig{Network: "openbox0", StoragePool: "default"})
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket})
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.Bootstrap(context.Background(), BootstrapConfig{Project: "openbox", Network: "openbox0", StoragePool: "default"})
	var drift *DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("error = %v, want DriftError", err)
	}
	if drift.Kind != "network" || !containsField(drift.Fields, "config.ipv4.nat") || !strings.Contains(err.Error(), "restore those fields") {
		t.Fatalf("drift error is not actionable: %#v, %v", drift, err)
	}
	if api.posts != 0 {
		t.Fatalf("bootstrap mutated resources before reporting drift: %d POSTs", api.posts)
	}
}

func TestRequiredDriftRequiresManagedBridgeGateway(t *testing.T) {
	desired := networkResource(BootstrapConfig{Network: "openbox0"})
	existing := resource{
		Name: "openbox0",
		Type: "bridge",
		Config: map[string]string{
			ManagedLabel: "true", ResourceLabel: "network",
			"ipv4.address": ManagedBridgeGateway, "ipv4.nat": "true", "ipv6.address": "none",
		},
	}
	if fields := requiredDrift(existing, desired); len(fields) != 0 {
		t.Fatalf("managed bridge gateway reported as drift: %v", fields)
	}
	existing.Config["ipv4.address"] = "10.42.1.1/24"
	if fields := requiredDrift(existing, desired); !containsField(fields, "config.ipv4.address") {
		t.Fatalf("fields = %v, want config.ipv4.address", fields)
	}
}

func TestNetworkResourcePinsManagedBridgeGateway(t *testing.T) {
	network := networkResource(BootstrapConfig{Network: "openbox0"})
	if got := network.Config["ipv4.address"]; got != "10.42.0.1/24" {
		t.Fatalf("ipv4.address = %q, want 10.42.0.1/24", got)
	}
}

func TestRequiredDriftDetectsManagedACLRuleChanges(t *testing.T) {
	desired := networkACLResource()
	existing := desired
	existing.Egress = append([]networkACLRule(nil), desired.Egress...)
	existing.Egress[0].DestinationPort = "5353"

	if fields := requiredDrift(existing, desired); !containsField(fields, "egress") {
		t.Fatalf("fields = %v, want egress", fields)
	}
}

func TestRequiredDriftValidatesProfileDevicesAndAllowsExtras(t *testing.T) {
	config := BootstrapConfig{Network: "openbox0", StoragePool: "default"}
	desired := profileResource("openbox-container", "container-profile", config)

	exactWithExtras := resource{
		Name:    "openbox-container",
		Config:  cloneStringMap(desired.Config),
		Devices: cloneDevices(desired.Devices),
	}
	exactWithExtras.Config["user.server-added"] = "keep"
	exactWithExtras.Devices["root"]["size.state"] = "keep"
	exactWithExtras.Devices["extra"] = map[string]string{"type": "none"}
	if fields := requiredDrift(exactWithExtras, desired); len(fields) != 0 {
		t.Fatalf("server-added fields reported as drift: %v", fields)
	}

	tests := []struct {
		name, field string
		mutate      func(resource) resource
	}{
		{name: "storage", field: "devices.root.pool", mutate: func(value resource) resource {
			value.Devices["root"]["pool"] = "other"
			return value
		}},
		{name: "network", field: "devices.eth0.network", mutate: func(value resource) resource {
			value.Devices["eth0"]["network"] = "other"
			return value
		}},
		{name: "missing root", field: "devices.root", mutate: func(value resource) resource {
			delete(value.Devices, "root")
			return value
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := resource{Name: desired.Name, Config: cloneStringMap(desired.Config), Devices: cloneDevices(desired.Devices)}
			fields := requiredDrift(test.mutate(value), desired)
			if !containsField(fields, test.field) {
				t.Fatalf("fields = %v, want %s", fields, test.field)
			}
		})
	}
}

func TestRealIncusPreflightAndBootstrap(t *testing.T) {
	socket := os.Getenv("OPENBOX_INCUS_TEST_SOCKET")
	if socket == "" {
		t.Skip("set OPENBOX_INCUS_TEST_SOCKET to run real-Incus integration tests")
	}
	adapter, err := New(Options{SocketPath: socket, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.DiscoverCapabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	pool := os.Getenv("OPENBOX_INCUS_TEST_STORAGE")
	if pool == "" {
		t.Skip("preflight passed; set OPENBOX_INCUS_TEST_STORAGE for destructive bootstrap isolation test")
	}
	stamp := time.Now().UTC().Format("20060102150405")
	config := (BootstrapConfig{Project: "openbox-test-" + stamp, Network: "ob-test-" + stamp, StoragePool: pool}).defaults()
	var defaultBefore json.RawMessage
	if err := adapter.request(context.Background(), http.MethodGet, "/1.0/projects/default", nil, nil, &defaultBefore); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Bootstrap(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupIntegrationResources(adapter, config) })
	if err := adapter.Bootstrap(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	var defaultAfter json.RawMessage
	if err := adapter.request(context.Background(), http.MethodGet, "/1.0/projects/default", nil, nil, &defaultAfter); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(defaultBefore, defaultAfter) {
		t.Fatal("bootstrap changed the unrelated default project")
	}
}

func cleanupIntegrationResources(adapter *Adapter, config BootstrapConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	query := mapQuery("project", config.Project)
	_ = adapter.request(ctx, http.MethodDelete, "/1.0/profiles/"+config.VMProfile, query, nil, nil)
	_ = adapter.request(ctx, http.MethodDelete, "/1.0/profiles/"+config.ContainerProfile, query, nil, nil)
	_ = adapter.request(ctx, http.MethodDelete, "/1.0/networks/"+config.Network, query, nil, nil)
	_ = adapter.request(ctx, http.MethodDelete, "/1.0/projects/"+config.Project, nil, nil, nil)
}

type bootstrapAPI struct {
	mu        sync.Mutex
	resources map[string]resource
	posts     int
}

func newBootstrapAPI() *bootstrapAPI {
	return &bootstrapAPI{resources: map[string]resource{
		"storage/default":   {Name: "default", Type: "dir"},
		"project/unrelated": {Name: "unrelated", Config: map[string]string{"user.owner": "someone-else"}},
	}}
}

func (a *bootstrapAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	kind, name, collection := classifyPath(r.URL.Path)
	key := kind + "/" + name
	if r.Method == http.MethodGet && !collection {
		value, exists := a.resources[key]
		if !exists {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeSync(w, value)
		return
	}
	if r.Method == http.MethodPost && collection {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var value resource
		if err := json.Unmarshal(body, &value); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.resources[kind+"/"+value.Name] = value
		a.posts++
		writeSync(w, nil)
		return
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (a *bootstrapAPI) snapshot() map[string]resource {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make(map[string]resource, len(a.resources))
	for key, value := range a.resources {
		value.Config = cloneStringMap(value.Config)
		value.Devices = cloneDevices(value.Devices)
		result[key] = value
	}
	return result
}

func classifyPath(path string) (kind, name string, collection bool) {
	parts := splitPath(path)
	if len(parts) < 2 {
		return "", "", false
	}
	switch parts[1] {
	case "projects":
		kind = "project"
	case "networks":
		kind = "network"
	case "profiles":
		kind = "profile"
	case "storage-pools":
		kind = "storage"
	case "network-acls":
		kind = "network-acl"
	}
	collection = len(parts) == 2
	if len(parts) > 2 {
		name = parts[2]
	}
	return kind, name, collection
}

func splitPath(path string) []string {
	return strings.FieldsFunc(path, func(character rune) bool { return character == '/' })
}

func cloneStringMap(value map[string]string) map[string]string {
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func cloneDevices(value map[string]map[string]string) map[string]map[string]string {
	result := make(map[string]map[string]string, len(value))
	for key, item := range value {
		result[key] = cloneStringMap(item)
	}
	return result
}

func containsField(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsNetworkACLRule(rules []networkACLRule, wanted networkACLRule) bool {
	for _, rule := range rules {
		if rule.Action == wanted.Action &&
			rule.Source == wanted.Source &&
			rule.Destination == wanted.Destination &&
			rule.Protocol == wanted.Protocol &&
			rule.DestinationPort == wanted.DestinationPort {
			return true
		}
	}
	return false
}

func serveUnixHTTP(t *testing.T, handler http.Handler) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "openbox-incus-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "incus.socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
		_ = listener.Close()
	})
	return socket
}

func writeSync(w http.ResponseWriter, metadata any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "sync", "status": "Success", "status_code": 200, "metadata": metadata})
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": message, "error_code": status})
}

func mapQuery(key, value string) map[string][]string {
	return map[string][]string{key: {value}}
}
