// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestVMHTTPLifecycleUsesPinnedImageAndStructuredDevices(t *testing.T) {
	api := &vmAPI{address: "192.0.2.10"}
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{
		SocketPath: socket, Project: "openbox", VMProfile: "openbox-vm", StoragePool: "default", Network: "openbox0",
		ReadinessTimeout: time.Second, ReadinessPoll: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter.sshProbe = func(ctx context.Context, address string) (bool, error) {
		return address == "192.0.2.10", nil
	}
	request := runtimeapi.CreateRequest{
		Ref: "obx-vm", Image: "sha256:vm", VM: true, OwnerPublicKey: "ssh-ed25519 owner",
		Metadata:  map[string]string{ManagedLabel: "true", ResourceLabel: "instance", InstanceIDLabel: "instance-1", OwnerIDLabel: "owner-1"},
		Resources: runtimeapi.Resources{VCPUs: 4, MemoryBytes: 4096, DiskBytes: 8192},
	}
	created, err := adapter.CreateInstance(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !created.IsVM || created.State != runtimeapi.StateStopped || created.Image != "sha256:vm" {
		t.Fatalf("created = %+v", created)
	}
	api.mu.Lock()
	posted := api.posted
	api.mu.Unlock()
	if posted.Type != "virtual-machine" || posted.Source["fingerprint"] != "sha256:vm" || len(posted.Profiles) != 1 || posted.Profiles[0] != "openbox-vm" {
		t.Fatalf("posted = %+v", posted)
	}
	if posted.Config["limits.cpu"] != "4" || posted.Config["limits.memory"] != "4096B" || !strings.Contains(posted.Config["cloud-init.user-data"], "ssh-ed25519 owner") {
		t.Fatalf("VM config = %+v", posted.Config)
	}
	if posted.Devices["root"]["pool"] != "default" || posted.Devices["root"]["size"] != "8192B" || posted.Devices["cloud-init"]["source"] != "cloud-init:config" || posted.Devices["eth0"]["network"] != "openbox0" {
		t.Fatalf("VM devices = %+v", posted.Devices)
	}
	if err := adapter.StartInstance(context.Background(), request.Ref); err != nil {
		t.Fatal(err)
	}
	var stages []string
	if err := adapter.WaitInstanceReady(context.Background(), runtimeapi.ReadinessRequest{Ref: request.Ref, Stage: func(stage string) error {
		stages = append(stages, stage)
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(stages, ",") != "waiting_for_agent,waiting_for_ssh" {
		t.Fatalf("stages = %v", stages)
	}
	if err := adapter.StopInstance(context.Background(), request.Ref); err != nil {
		t.Fatal(err)
	}
	if err := adapter.DeleteInstance(context.Background(), request.Ref); err != nil {
		t.Fatal(err)
	}
}

func TestInstanceSSHAddressRequiresManagedPrivateSubnet(t *testing.T) {
	api := &vmAPI{address: "10.42.0.12", networkAddress: "10.42.0.1/24"}
	adapter := newVMTestAdapter(t, api, time.Second)
	address, err := adapter.InstanceSSHAddress(context.Background(), "obx-instance")
	if err != nil || address != "10.42.0.12" {
		t.Fatalf("address=%q err=%v", address, err)
	}
	api.mu.Lock()
	api.address = "10.99.0.12"
	api.mu.Unlock()
	if _, err := adapter.InstanceSSHAddress(context.Background(), "obx-instance"); err == nil {
		t.Fatal("private address outside managed subnet accepted")
	}
}

func TestVMCreateRejectsMutableOrIncompatibleImageBeforePOST(t *testing.T) {
	tests := []struct {
		name       string
		request    string
		imageType  string
		properties map[string]string
	}{
		{name: "mutable alias", request: "ubuntu", imageType: "virtual-machine", properties: map[string]string{"variant": "cloud"}},
		{name: "container image", request: "sha256:vm", imageType: "container", properties: map[string]string{"variant": "cloud"}},
		{name: "no cloud init", request: "sha256:vm", imageType: "virtual-machine", properties: map[string]string{"variant": "default"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := &vmAPI{imageType: test.imageType, imageProperties: test.properties}
			socket := serveUnixHTTP(t, api)
			adapter, err := New(Options{SocketPath: socket, StoragePool: "default"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.CreateInstance(context.Background(), runtimeapi.CreateRequest{
				Ref: "obx-vm", Image: test.request, VM: true, OwnerPublicKey: "ssh-ed25519 owner",
				Metadata: map[string]string{ManagedLabel: "true", ResourceLabel: "instance", InstanceIDLabel: "instance-1", OwnerIDLabel: "owner-1"},
			})
			if err == nil {
				t.Fatal("incompatible VM image was accepted")
			}
			api.mu.Lock()
			posts := api.posts
			api.mu.Unlock()
			if posts != 0 {
				t.Fatalf("image validation happened after %d POSTs", posts)
			}
		})
	}
}

func TestVMReadinessTimeoutsAreBoundedAndCancellable(t *testing.T) {
	t.Run("agent", func(t *testing.T) {
		api := &vmAPI{}
		adapter := newVMTestAdapter(t, api, 25*time.Millisecond)
		err := adapter.WaitInstanceReady(context.Background(), runtimeapi.ReadinessRequest{Ref: "obx-vm"})
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "waiting_for_agent") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("ssh", func(t *testing.T) {
		api := &vmAPI{address: "192.0.2.10"}
		adapter := newVMTestAdapter(t, api, 25*time.Millisecond)
		adapter.sshProbe = func(context.Context, string) (bool, error) { return false, errors.New("connection refused") }
		err := adapter.WaitInstanceReady(context.Background(), runtimeapi.ReadinessRequest{Ref: "obx-vm"})
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "waiting_for_ssh") || !strings.Contains(err.Error(), "connection refused") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		adapter := newVMTestAdapter(t, &vmAPI{}, time.Second)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := adapter.WaitInstanceReady(ctx, runtimeapi.ReadinessRequest{Ref: "obx-vm"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestVMCapabilityMatrix(t *testing.T) {
	tests := []struct {
		name       string
		host       HostCapabilities
		extension  bool
		wantStatus runtimeapi.VMAvailability
		wantUsable bool
		wantKVM    bool
	}{
		{name: "KVM absent", host: HostCapabilities{VMAvailability: runtimeapi.VMUnavailableKVMAbsent, VMReason: "absent"}, extension: true, wantStatus: runtimeapi.VMUnavailableKVMAbsent},
		{name: "permission denied", host: HostCapabilities{VMAvailability: runtimeapi.VMUnavailableKVMPermission, VMReason: "permission denied"}, extension: true, wantStatus: runtimeapi.VMUnavailableKVMPermission},
		{name: "nested unavailable", host: HostCapabilities{VMAvailability: runtimeapi.VMUnavailableNestedVirtualization, VMReason: "nested disabled"}, extension: true, wantStatus: runtimeapi.VMUnavailableNestedVirtualization},
		{name: "supported", host: HostCapabilities{KVM: true, VMAvailability: runtimeapi.VMAvailable}, extension: true, wantStatus: runtimeapi.VMAvailable, wantUsable: true, wantKVM: true},
		{name: "Incus unsupported", host: HostCapabilities{KVM: true, VMAvailability: runtimeapi.VMAvailable}, extension: false, wantStatus: runtimeapi.VMUnavailableIncus, wantKVM: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			extensions := []string(nil)
			if test.extension {
				extensions = []string{"virtual-machines"}
			}
			socket := serveUnixHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeSync(w, map[string]any{"api_extensions": extensions, "environment": map[string]any{"kernel_architecture": "x86_64", "server_version": "6.23"}})
			}))
			adapter, err := New(Options{SocketPath: socket, HostProbe: staticProbe{capabilities: test.host}})
			if err != nil {
				t.Fatal(err)
			}
			capabilities, err := adapter.DiscoverCapabilities(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if capabilities.VMAvailability != test.wantStatus || capabilities.VirtualMachines != test.wantUsable || capabilities.KVM != test.wantKVM {
				t.Fatalf("capabilities = %+v", capabilities)
			}
		})
	}
}

func TestKVMAPIVersionClassificationRequiresStableVersion12(t *testing.T) {
	tests := []struct {
		version uintptr
		err     error
		want    runtimeapi.VMAvailability
	}{
		{version: 12, want: runtimeapi.VMAvailable},
		{version: 0, want: runtimeapi.VMUnavailableNestedVirtualization},
		{version: 11, want: runtimeapi.VMUnavailableNestedVirtualization},
		{version: 13, want: runtimeapi.VMUnavailableNestedVirtualization},
		{version: 12, err: errors.New("ioctl failed"), want: runtimeapi.VMUnavailableNestedVirtualization},
	}
	for _, test := range tests {
		availability, reason := classifyKVMAPIVersion(test.version, test.err)
		if availability != test.want {
			t.Fatalf("version=%d err=%v availability=%s reason=%q", test.version, test.err, availability, reason)
		}
		if availability != runtimeapi.VMAvailable && reason == "" {
			t.Fatalf("version=%d err=%v returned no reason", test.version, test.err)
		}
	}
}

func TestRealIncusVMLifecycleAndRestart(t *testing.T) {
	socket := os.Getenv("OPENBOX_INCUS_TEST_SOCKET")
	pool := os.Getenv("OPENBOX_INCUS_TEST_STORAGE")
	image := os.Getenv("OPENBOX_INCUS_TEST_VM_IMAGE")
	if socket == "" || pool == "" || image == "" {
		t.Skip("set OPENBOX_INCUS_TEST_SOCKET, OPENBOX_INCUS_TEST_STORAGE, and OPENBOX_INCUS_TEST_VM_IMAGE to run real KVM lifecycle")
	}
	stamp := time.Now().UTC().Format("20060102150405")
	config := (BootstrapConfig{Project: "openbox-vm-" + stamp, Network: "obvm-" + stamp, StoragePool: pool, ContainerProfile: "obvmc-" + stamp, VMProfile: "obvmv-" + stamp}).defaults()
	bootstrap, err := New(Options{SocketPath: socket, Timeout: 60 * time.Second, OperationTimeout: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := bootstrap.DiscoverCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.VirtualMachines || capabilities.VMAvailability != runtimeapi.VMAvailable {
		t.Fatalf("real host does not provide usable KVM: %+v", capabilities)
	}
	if err := bootstrap.Bootstrap(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupIntegrationResources(bootstrap, config) })
	adapter, err := New(Options{
		SocketPath: socket, Timeout: 60 * time.Second, OperationTimeout: 10 * time.Minute,
		ReadinessTimeout: 10 * time.Minute, Project: config.Project, VMProfile: config.VMProfile,
		StoragePool: pool, Network: config.Network,
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := "obx-vm-" + stamp
	request := runtimeapi.CreateRequest{
		Ref: ref, Image: image, VM: true, OwnerPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOpenBoxVMLifecycleTest",
		Metadata:  map[string]string{ManagedLabel: "true", ResourceLabel: "instance", InstanceIDLabel: "integration-vm", OwnerIDLabel: "integration-owner"},
		Resources: runtimeapi.Resources{VCPUs: 2, MemoryBytes: 2 << 30, DiskBytes: 10 << 30},
	}
	if _, err := adapter.CreateInstance(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = adapter.StopInstance(context.Background(), ref)
		_ = adapter.DeleteInstance(context.Background(), ref)
	})
	for cycle := 0; cycle < 2; cycle++ {
		if err := adapter.StartInstance(context.Background(), ref); err != nil {
			t.Fatal(err)
		}
		if err := adapter.WaitInstanceReady(context.Background(), runtimeapi.ReadinessRequest{Ref: ref}); err != nil {
			t.Fatal(err)
		}
		instance, err := adapter.InspectInstance(context.Background(), ref)
		if err != nil || !instance.IsVM || instance.State != runtimeapi.StateRunning || instance.Metadata[InstanceIDLabel] != "integration-vm" {
			t.Fatalf("cycle %d instance=%+v err=%v", cycle, instance, err)
		}
		if err := adapter.StopInstance(context.Background(), ref); err != nil {
			t.Fatal(err)
		}
	}
	if err := adapter.DeleteInstance(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
}

func newVMTestAdapter(t *testing.T, api *vmAPI, timeout time.Duration) *Adapter {
	t.Helper()
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket, ReadinessTimeout: timeout, ReadinessPoll: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

type postedVM struct {
	Name     string                       `json:"name"`
	Type     string                       `json:"type"`
	Source   map[string]string            `json:"source"`
	Profiles []string                     `json:"profiles"`
	Config   map[string]string            `json:"config"`
	Devices  map[string]map[string]string `json:"devices"`
}

type vmAPI struct {
	mu              sync.Mutex
	posted          postedVM
	posts           int
	instance        *instanceRecord
	address         string
	imageType       string
	imageProperties map[string]string
	networkAddress  string
}

func (a *vmAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/1.0/networks/"):
		address := a.networkAddress
		if address == "" {
			address = "10.42.0.1/24"
		}
		writeSync(w, resource{Name: "openbox0", Config: map[string]string{"ipv4.address": address}})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/1.0/images/"):
		imageType := a.imageType
		if imageType == "" {
			imageType = "virtual-machine"
		}
		properties := a.imageProperties
		if properties == nil {
			properties = map[string]string{"variant": "cloud"}
		}
		writeSync(w, imageRecord{Fingerprint: "sha256:vm", Architecture: "x86_64", Type: imageType, Properties: properties})
	case r.Method == http.MethodPost && r.URL.Path == "/1.0/instances":
		if err := json.NewDecoder(r.Body).Decode(&a.posted); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.posts++
		config := cloneStringMap(a.posted.Config)
		config["volatile.base_image"] = a.posted.Source["fingerprint"]
		a.instance = &instanceRecord{Name: a.posted.Name, Type: a.posted.Type, Status: "Stopped", Config: config, ExpandedConfig: config}
		writeSync(w, nil)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exec"):
		writeSync(w, nil)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/state"):
		if a.instance == nil && a.address == "" {
			writeSync(w, instanceStateRecord{})
			return
		}
		state := instanceStateRecord{Network: map[string]instanceStateNetwork{}}
		if a.address != "" {
			state.Network["eth0"] = instanceStateNetwork{
				Addresses: []instanceStateAddress{{Family: "inet", Address: a.address, Scope: "global"}},
			}
		}
		writeSync(w, state)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/1.0/instances/"):
		if a.instance == nil {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeSync(w, a.instance)
	case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/state"):
		if a.instance == nil {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		var body struct {
			Action string `json:"action"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Action == "start" {
			a.instance.Status = "Running"
		} else {
			a.instance.Status = "Stopped"
		}
		writeSync(w, nil)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/1.0/instances/"):
		a.instance = nil
		writeSync(w, nil)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}
