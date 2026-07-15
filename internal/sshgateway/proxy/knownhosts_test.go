// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestTOFUHostKeysPinsAndRejectsChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh", "known_instances")
	store, err := NewTOFUHostKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	first := signer(t).PublicKey()
	address := &net.TCPAddr{IP: net.ParseIP("10.10.0.2"), Port: 22}
	if err := store.Callback("10.10.0.2:22", address, first); err != nil {
		t.Fatal(err)
	}
	if err := store.Callback("10.10.0.2:22", address, first); err != nil {
		t.Fatalf("pinned key rejected: %v", err)
	}
	if err := store.Callback("10.10.0.2:22", address, signer(t).PublicKey()); err == nil {
		t.Fatal("changed host key accepted")
	}
	if body, err := os.ReadFile(path); err != nil || len(body) == 0 {
		t.Fatalf("known hosts body=%q err=%v", body, err)
	}
}

func TestTOFUHostKeysRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "known")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	store, _ := NewTOFUHostKeys(path)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	public, _ := ssh.NewPublicKey(key.Public())
	if err := store.Callback("host:22", &net.TCPAddr{}, public); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestNewTOFUHostKeysRefusesWritableDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "writable")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTOFUHostKeys(filepath.Join(directory, "known")); err == nil || !strings.Contains(err.Error(), "group- or other-writable") {
		t.Fatalf("writable directory error = %v", err)
	}
}

func TestNewTOFUHostKeysRefusesSymlinkDirectory(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTOFUHostKeys(filepath.Join(link, "known")); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink directory error = %v", err)
	}
}

func TestTOFUHostKeysDetectsDirectoryReplacementBetweenCallbacks(t *testing.T) {
	base := t.TempDir()
	directory := filepath.Join(base, "ssh")
	path := filepath.Join(directory, "known")
	store, err := NewTOFUHostKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	key := signer(t).PublicKey()
	address := &net.TCPAddr{IP: net.ParseIP("10.10.0.2"), Port: 22}
	if err := store.Callback("10.10.0.2:22", address, key); err != nil {
		t.Fatal(err)
	}
	oldDirectory := filepath.Join(base, "old-ssh")
	if err := os.Rename(directory, oldDirectory); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	oldBody, err := os.ReadFile(filepath.Join(oldDirectory, "known"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, oldBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Callback("10.10.0.2:22", address, key); err == nil || !strings.Contains(err.Error(), "changed after initialization") {
		t.Fatalf("directory replacement error = %v", err)
	}
}
