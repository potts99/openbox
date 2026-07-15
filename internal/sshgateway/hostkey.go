// SPDX-License-Identifier: AGPL-3.0-only

package sshgateway

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

func LoadOrCreateHostKey(path string) (ssh.Signer, error) {
	if path == "" {
		return nil, errors.New("SSH host key path is required")
	}
	if err := EnsureSafeStorageDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("SSH host key %q must not be a symbolic link", path)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("SSH host key %q must be a regular owner-only file", path)
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return nil, fmt.Errorf("open SSH host key: %w", openErr)
		}
		defer file.Close()
		opened, statErr := file.Stat()
		if statErr != nil {
			return nil, fmt.Errorf("inspect opened SSH host key: %w", statErr)
		}
		if !os.SameFile(info, opened) {
			return nil, errors.New("SSH host key changed while opening")
		}
		data, readErr := io.ReadAll(file)
		if readErr != nil {
			return nil, fmt.Errorf("read SSH host key: %w", readErr)
		}
		return ssh.ParsePrivateKey(data)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect SSH host key: %w", err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate SSH host key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("encode SSH host key: %w", err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	temporary, err := os.CreateTemp(filepath.Dir(path), ".openbox-host-key-*")
	if err != nil {
		return nil, fmt.Errorf("create SSH host key: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err != nil {
		return nil, fmt.Errorf("persist SSH host key: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close SSH host key: %w", closeErr)
	}
	// Link is atomic and, unlike Rename, never replaces an existing host key.
	if err := os.Link(temporaryName, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("install SSH host key: %w", err)
		}
		return LoadOrCreateHostKey(path)
	}
	return ssh.ParsePrivateKey(data)
}

// EnsureSafeStorageDirectory creates an absent private storage directory and
// refuses one that another local user could replace or write through.
func EnsureSafeStorageDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("SSH host key directory %q must not be a symbolic link", path)
		}
		if !info.IsDir() {
			return fmt.Errorf("SSH host key directory %q is not a directory", path)
		}
		if info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("SSH host key directory %q must not be group- or other-writable", path)
		}
		if err := validateDirectoryOwner(info); err != nil {
			return fmt.Errorf("unsafe SSH host key directory %q: %w", path, err)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect SSH host key directory: %w", err)
	}
	parent := filepath.Dir(path)
	if parent == path {
		return fmt.Errorf("SSH host key directory %q does not exist", path)
	}
	if err := EnsureSafeStorageDirectory(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create SSH host key directory: %w", err)
	}
	// Re-check after creation, including a racing replacement.
	return EnsureSafeStorageDirectory(path)
}
