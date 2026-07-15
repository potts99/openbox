// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHConfigPrintDoesNotContactAPI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"ssh-config", "print", "--host", "box.example", "--port", "2202", "--alias", "obx"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"Host obx", "HostName box.example", "Port 2202", "Host *.openbox", "User %n"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestSSHConfigInstallIsIdempotentAndRefusesCollisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ssh", "config")
	args := []string{"ssh-config", "install", "--host", "box.example", "--config", path}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("first install exit=%d stderr=%q", code, stderr.String())
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(args, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "no changes") {
		t.Fatalf("repeat exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(body, after) {
		t.Fatal("repeat install changed config")
	}

	collision := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(collision, []byte("Host openbox\n  HostName existing.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"ssh-config", "install", "--host", "new.example", "--config", collision}, &stdout, &stderr); code == 0 {
		t.Fatal("install overwrote an existing alias")
	}
	existing, _ := os.ReadFile(collision)
	if strings.Contains(string(existing), "new.example") {
		t.Fatal("collision changed existing config")
	}
}

func TestSSHConfigInstallRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("Host safe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"ssh-config", "install", "--host", "box.example", "--config", link}, &stdout, &stderr); code == 0 {
		t.Fatal("symlink accepted")
	}
	body, _ := os.ReadFile(target)
	if string(body) != "Host safe\n" {
		t.Fatal("symlink target changed")
	}
}
