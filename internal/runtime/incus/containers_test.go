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

func TestContainerHTTPLifecycleUsesStructuredIncusAPI(t *testing.T) {
	api := &containerAPI{}
	socket := serveUnixHTTP(t, api)
	adapter, err := New(Options{SocketPath: socket, Project: "openbox", ContainerProfile: "openbox-container", StoragePool: "default"})
	if err != nil {
		t.Fatal(err)
	}
	images, err := adapter.ListImages(context.Background())
	if err != nil || len(images) != 1 || images[0].Fingerprint != "sha256:base" {
		t.Fatalf("images=%+v err=%v", images, err)
	}
	request := runtimeapi.CreateRequest{
		Ref: "obx-ref", Image: images[0].Fingerprint, Unprivileged: true, OwnerPublicKey: "ssh-ed25519 owner",
		Metadata:  map[string]string{ManagedLabel: "true", ResourceLabel: "instance", InstanceIDLabel: "instance-1", OwnerIDLabel: "owner-1"},
		Resources: runtimeapi.Resources{VCPUs: 2, MemoryBytes: 1024, DiskBytes: 2048},
	}
	created, err := adapter.CreateInstance(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if created.State != runtimeapi.StateStopped || created.IsVM || created.Privileged {
		t.Fatalf("created=%+v", created)
	}
	listed, err := adapter.ListInstances(context.Background())
	if err != nil || len(listed) != 1 || listed[0].Ref != created.Ref || listed[0].Metadata[InstanceIDLabel] != "instance-1" {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	api.mu.Lock()
	posted := api.posted
	api.mu.Unlock()
	if posted.Type != "container" || posted.Source["fingerprint"] != "sha256:base" || posted.Config["security.privileged"] != "false" {
		t.Fatalf("posted=%+v", posted)
	}
	if posted.Config["limits.cpu"] != "2" || posted.Config["limits.memory"] != "1024B" || posted.Devices["root"]["size"] != "2048B" {
		t.Fatalf("resources config=%v devices=%v", posted.Config, posted.Devices)
	}
	if !strings.Contains(posted.Config["cloud-init.user-data"], "ssh-ed25519 owner") {
		t.Fatalf("owner key missing: %s", posted.Config["cloud-init.user-data"])
	}
	if err := adapter.StartInstance(context.Background(), "obx-ref"); err != nil {
		t.Fatal(err)
	}
	inspected, err := adapter.InspectInstance(context.Background(), "obx-ref")
	if err != nil || inspected.State != runtimeapi.StateRunning {
		t.Fatalf("running=%+v err=%v", inspected, err)
	}
	if err := adapter.StopInstance(context.Background(), "obx-ref"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.DeleteInstance(context.Background(), "obx-ref"); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.InspectInstance(context.Background(), "obx-ref"); !errors.Is(err, runtimeapi.ErrNotFound) {
		t.Fatalf("inspect deleted=%v", err)
	}
}

func TestContainerCreateRejectsVMAndPrivilegedRequestsBeforeHTTP(t *testing.T) {
	adapter, err := New(Options{SocketPath: "/tmp/not-used.socket"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.CreateInstance(context.Background(), runtimeapi.CreateRequest{Ref: "vm", Image: "sha256:x", VM: true}); !errors.Is(err, runtimeapi.ErrUnsupported) {
		t.Fatalf("VM error=%v", err)
	}
	if _, err := adapter.CreateInstance(context.Background(), runtimeapi.CreateRequest{Ref: "privileged", Image: "sha256:x"}); !errors.Is(err, runtimeapi.ErrUnsupported) {
		t.Fatalf("privileged error=%v", err)
	}
}

func TestImageCloudInitCompatibilityProperties(t *testing.T) {
	tests := []struct {
		name       string
		properties map[string]string
		want       bool
	}{
		{name: "official cloud variant", properties: map[string]string{"variant": "cloud"}, want: true},
		{name: "explicit OpenBox override", properties: map[string]string{CloudInitOverrideProperty: "true"}, want: true},
		{name: "default image", properties: map[string]string{"variant": "default"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageSupportsCloudInit(tt.properties); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

type postedContainer struct {
	Name     string                       `json:"name"`
	Type     string                       `json:"type"`
	Source   map[string]string            `json:"source"`
	Profiles []string                     `json:"profiles"`
	Config   map[string]string            `json:"config"`
	Devices  map[string]map[string]string `json:"devices"`
}

type containerAPI struct {
	mu       sync.Mutex
	instance *instanceRecord
	posted   postedContainer
}

func (a *containerAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/1.0/images":
		writeSync(w, []imageRecord{{Fingerprint: "sha256:base", Architecture: "x86_64", Type: "container", Properties: map[string]string{"variant": "cloud"}, Aliases: []struct {
			Name string `json:"name"`
		}{{Name: "base"}}}})
	case r.Method == http.MethodPost && r.URL.Path == "/1.0/instances":
		if err := json.NewDecoder(r.Body).Decode(&a.posted); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		config := cloneStringMap(a.posted.Config)
		config["volatile.base_image"] = a.posted.Source["fingerprint"]
		a.instance = &instanceRecord{Name: a.posted.Name, Type: a.posted.Type, Status: "Stopped", Config: config, ExpandedConfig: config}
		writeSync(w, nil)
	case r.Method == http.MethodGet && r.URL.Path == "/1.0/instances":
		if a.instance == nil {
			writeSync(w, []instanceRecord{})
			return
		}
		writeSync(w, []instanceRecord{*a.instance})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/1.0/instances/") && !strings.HasSuffix(r.URL.Path, "/state"):
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
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if body.Action == "start" {
			a.instance.Status = "Running"
		} else {
			a.instance.Status = "Stopped"
		}
		writeSync(w, nil)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/1.0/instances/"):
		if a.instance == nil {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		a.instance = nil
		writeSync(w, nil)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func TestRealIncusContainerLifecycleAndReconnect(t *testing.T) {
	socket := os.Getenv("OPENBOX_INCUS_TEST_SOCKET")
	pool := os.Getenv("OPENBOX_INCUS_TEST_STORAGE")
	image := os.Getenv("OPENBOX_INCUS_TEST_IMAGE")
	if socket == "" || pool == "" || image == "" {
		t.Skip("set OPENBOX_INCUS_TEST_SOCKET, OPENBOX_INCUS_TEST_STORAGE, and OPENBOX_INCUS_TEST_IMAGE to run real container lifecycle")
	}
	stamp := time.Now().UTC().Format("20060102150405")
	config := (BootstrapConfig{Project: "openbox-lifecycle-" + stamp, Network: "obl-" + stamp, StoragePool: pool, ContainerProfile: "oblc-" + stamp, VMProfile: "oblv-" + stamp}).defaults()
	bootstrap, err := New(Options{SocketPath: socket, Timeout: 60 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Bootstrap(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupIntegrationResources(bootstrap, config) })
	options := Options{SocketPath: socket, Timeout: 60 * time.Second, Project: config.Project, ContainerProfile: config.ContainerProfile, StoragePool: pool}
	adapter, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	ref := "obx-lifecycle-" + stamp
	request := runtimeapi.CreateRequest{Ref: ref, Image: image, Unprivileged: true, OwnerPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOpenBoxLifecycleTest",
		Metadata: map[string]string{ManagedLabel: "true", ResourceLabel: "instance", InstanceIDLabel: "integration-instance", OwnerIDLabel: "integration-owner"}}
	if _, err := adapter.CreateInstance(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = adapter.StopInstance(context.Background(), ref)
		_ = adapter.DeleteInstance(context.Background(), ref)
	})
	if err := adapter.StartInstance(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	var raw instanceRecord
	if err := adapter.request(context.Background(), http.MethodGet, "/1.0/instances/"+ref, mapQuery("project", config.Project), nil, &raw); err != nil {
		t.Fatal(err)
	}
	idmap := raw.ExpandedConfig["volatile.idmap.current"]
	if idmap == "" {
		idmap = raw.Config["volatile.idmap.current"]
	}
	var mappings []struct {
		HostID int64 `json:"Hostid"`
		NSID   int64 `json:"Nsid"`
	}
	if err := json.Unmarshal([]byte(idmap), &mappings); err != nil {
		t.Fatalf("parse id map %q: %v", idmap, err)
	}
	unprivileged := false
	for _, mapping := range mappings {
		if mapping.NSID == 0 && mapping.HostID > 0 {
			unprivileged = true
		}
	}
	if !unprivileged {
		t.Fatalf("guest root is not mapped to an unprivileged host id: %s", idmap)
	}
	reconnected, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if instance, err := reconnected.InspectInstance(context.Background(), ref); err != nil || instance.Metadata[InstanceIDLabel] != "integration-instance" {
		t.Fatalf("reconnect instance=%+v err=%v", instance, err)
	}
	if err := reconnected.StopInstance(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if err := reconnected.DeleteInstance(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
}
