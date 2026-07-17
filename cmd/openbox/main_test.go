// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorHumanAndJSON(t *testing.T) {
	server := apiServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/health":
			w.Header().Set("X-OpenBox-API-Version", "v1")
			_, _ = fmt.Fprint(w, `{"status":"ok","server_version":"v0.1.0","api_version":"v1"}`)
		case "/v1/capabilities":
			_, _ = fmt.Fprint(w, `{"architecture":"x86_64","containers":true,"virtual_machines":false,"vm_availability":"kvm_absent","vm_reason":"KVM unavailable"}`)
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()

	stdout, stderr, code := runCLI(t, server.URL, "doctor")
	if code != 0 || !strings.Contains(stdout, "OpenBox v0.1.0") || !strings.Contains(stdout, "KVM unavailable") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = runCLI(t, server.URL, "doctor", "--json")
	if code != 0 || !json.Valid([]byte(stdout)) || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestListHumanAndJSON(t *testing.T) {
	server := commandServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/instances" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `{"items":[{"id":"box-1","name":"my-box","kind":"vps","requested_isolation":"strong","desired_state":"running","observed_state":"running","actual_isolation":"container"}]}`)
	})
	defer server.Close()

	stdout, stderr, code := runCLI(t, server.URL, "ls")
	if code != 0 || !strings.Contains(stdout, "my-box") || !strings.Contains(stdout, "RUNNING") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, _, code = runCLI(t, server.URL, "ls", "--json")
	if code != 0 || !strings.Contains(stdout, `"instances"`) || !json.Valid([]byte(stdout)) {
		t.Fatalf("exit=%d stdout=%q", code, stdout)
	}
}

func TestUsesBearerTokenFromFlag(t *testing.T) {
	server := commandServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer owner-token" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = fmt.Fprint(w, `{"items":[]}`)
	})
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"ls", "--server", server.URL, "--token", "owner-token"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestInspectHuman(t *testing.T) {
	server := commandServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"id":"box-1","name":"my-box","kind":"vps","image_id":"ubuntu","requested_isolation":"strong","desired_state":"stopped","observed_state":"stopped","actual_isolation":"virtual_machine","resources":{"vcpus":2,"memory_bytes":8589934592,"disk_bytes":10737418240},"network_policy":{"egress_mode":"standard","acls":["openbox-default-deny","openbox-egress-standard"],"resolution":{"state":"idle","pending":[],"resolved":[],"failed":[]},"denied_flows":0}}`)
	})
	defer server.Close()

	stdout, stderr, code := runCLI(t, server.URL, "inspect", "box-1")
	if code != 0 || !strings.Contains(stdout, "Isolation: virtual_machine") || !strings.Contains(stdout, "Memory: 8.0 GiB") || !strings.Contains(stdout, "Egress: standard") || !strings.Contains(stdout, "Network ACLs: openbox-default-deny, openbox-egress-standard") || !strings.Contains(stdout, "Hostname resolution: idle") || !strings.Contains(stdout, "Denied flows: 0") || !strings.Contains(stdout, "Observed: stopped") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestNewSendsRequestAndIdempotencyKey(t *testing.T) {
	server := commandServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/instances" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got == "" {
			t.Fatal("missing idempotency key")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["name"] != "my-box" || body["kind"] != "vps" || body["image"] != "ubuntu" || body["owner_public_key"] != "ssh-ed25519 test" {
			t.Fatalf("body = %#v", body)
		}
		_, _ = fmt.Fprint(w, `{"operation":{"id":"op-1","status":"pending","stage":"queued"},"instance":{"id":"box-1","name":"my-box","kind":"vps","requested_isolation":"strong","desired_state":"running","observed_state":"pending","actual_isolation":"unknown"}}`)
	})
	defer server.Close()

	stdout, stderr, code := runCLI(t, server.URL, "new", "my-box", "--kind", "vps", "--image", "ubuntu", "--ssh-key", "ssh-ed25519 test")
	if code != 0 || !strings.Contains(stdout, "op-1") || !strings.Contains(stdout, "box-1") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestSnapshotCreateSendsCheckpointRequest(t *testing.T) {
	server := commandServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/instances/box-1/snapshots" || r.Header.Get("Idempotency-Key") == "" {
			t.Fatalf("request = %s %s key=%q", r.Method, r.URL.Path, r.Header.Get("Idempotency-Key"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["name"] != "ready" {
			t.Fatalf("body=%v", body)
		}
		_, _ = fmt.Fprint(w, `{"snapshot":{"id":"snap-1","instance_id":"box-1","name":"ready","ready":false,"created_at":"2026-07-16T12:00:00Z"},"operation":{"id":"op-1","status":"pending"}}`)
	})
	defer server.Close()
	stdout, stderr, code := runCLI(t, server.URL, "snapshot", "create", "box-1", "ready")
	if code != 0 || !strings.Contains(stdout, "snap-1") || !strings.Contains(stdout, "op-1") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestNewSandboxSendsLifetimeAndEgressProfile(t *testing.T) {
	server := commandServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["kind"] != "sandbox" {
			t.Fatalf("kind=%v", body["kind"])
		}
		if body["lifetime_seconds"] != float64(7200) {
			t.Fatalf("lifetime_seconds=%v", body["lifetime_seconds"])
		}
		if body["egress_profile_id"] != "egress-system-sandbox" {
			t.Fatalf("egress_profile_id=%v", body["egress_profile_id"])
		}
		_, _ = fmt.Fprint(w, `{"operation":{"id":"op-1","status":"pending","stage":"queued"},"instance":{"id":"box-1","name":"agent","kind":"sandbox","requested_isolation":"container","desired_state":"running","observed_state":"pending","actual_isolation":"container"}}`)
	})
	defer server.Close()

	stdout, stderr, code := runCLI(t, server.URL,
		"new", "agent", "--kind", "sandbox", "--lifetime", "2h",
		"--egress-profile", "egress-system-sandbox", "--ssh-key", "ssh-ed25519 test",
	)
	if code != 0 || !strings.Contains(stdout, "op-1") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestLifecycleCommands(t *testing.T) {
	for _, command := range []string{"start", "stop", "restart", "rm"} {
		t.Run(command, func(t *testing.T) {
			server := commandServer(t, func(w http.ResponseWriter, r *http.Request) {
				wantMethod, wantPath := http.MethodPost, "/v1/instances/box-1/actions/"+command
				if command == "rm" {
					wantMethod, wantPath = http.MethodDelete, "/v1/instances/box-1"
				}
				if r.Method != wantMethod || r.URL.Path != wantPath || r.Header.Get("Idempotency-Key") == "" {
					t.Fatalf("request = %s %s key=%q", r.Method, r.URL.Path, r.Header.Get("Idempotency-Key"))
				}
				_, _ = fmt.Fprint(w, `{"id":"op-1","status":"pending"}`)
			})
			defer server.Close()

			stdout, stderr, code := runCLI(t, server.URL, command, "box-1", "--json")
			if code != 0 || !json.Valid([]byte(stdout)) || !strings.Contains(stdout, "op-1") {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestOperationWatchHumanAndJSON(t *testing.T) {
	server := commandServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/operations/op-1/events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "id: 1\nevent: operation\ndata: {\"sequence\":1,\"operation_id\":\"op-1\",\"status\":\"running\",\"stage\":\"booting\",\"progress\":50}\n\nid: 2\nevent: operation\ndata: {\"sequence\":2,\"operation_id\":\"op-1\",\"status\":\"succeeded\",\"stage\":\"complete\",\"progress\":100}\n\n")
	})
	defer server.Close()

	stdout, stderr, code := runCLI(t, server.URL, "operation", "watch", "op-1")
	if code != 0 || !strings.Contains(stdout, "booting") || !strings.Contains(stdout, "SUCCEEDED") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, _, code = runCLI(t, server.URL, "operation", "watch", "op-1", "--json")
	if code != 0 || strings.Count(strings.TrimSpace(stdout), "\n") != 1 {
		t.Fatalf("exit=%d stdout=%q", code, stdout)
	}
}

func TestAPIErrorIsActionable(t *testing.T) {
	server := commandServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"error":{"code":"not_found","message":"instance not found","request_id":"req-1"}}`)
	})
	defer server.Close()

	_, stderr, code := runCLI(t, server.URL, "inspect", "missing")
	if code != 1 || !strings.Contains(stderr, "instance not found") || !strings.Contains(stderr, "req-1") {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
}

func TestBackupCreateAndVerify(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "state", "openbox.db")
	if err := os.MkdirAll(filepath.Join(root, "state", "ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "state", "caddy"), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE fixture (value TEXT); INSERT INTO fixture VALUES ('preserved')"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"gateway_host":    "gateway private key",
		"instance_client": "instance private key",
		"known_instances": "instance.example ssh-ed25519 key",
	} {
		if err := os.WriteFile(filepath.Join(root, "state", "ssh", name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "state", "caddy", "routes.caddyfile"), []byte("route.example { reverse_proxy 127.0.0.1:8080 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "openboxd.env")
	if err := os.WriteFile(configPath, []byte("OPENBOX_STORAGE_POOL=openbox\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(root, "backup")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"backup", "create", backupPath,
		"--database", databasePath,
		"--state-dir", filepath.Join(root, "state"),
		"--config", configPath,
	}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "files: 6") || stderr.Len() != 0 {
		t.Fatalf("create exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"backup", "verify", backupPath}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "backup verified") || stderr.Len() != 0 {
		t.Fatalf("verify exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if err := os.WriteFile(filepath.Join(backupPath, "ssh", "gateway_host"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"backup", "verify", backupPath}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "integrity check failed") {
		t.Fatalf("corrupt verify exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func commandServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return apiServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			w.Header().Set("X-OpenBox-API-Version", "v1")
			_, _ = fmt.Fprint(w, `{"status":"ok","server_version":"test","api_version":"v1"}`)
			return
		}
		handler(w, r)
	})
}

func apiServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-OpenBox-API-Version") != "v1" {
			t.Fatalf("API version = %q", r.Header.Get("X-OpenBox-API-Version"))
		}
		handler(w, r)
	}))
}

func runCLI(t *testing.T, serverURL string, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args = append(args, "--server", serverURL)
	code := run(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}
