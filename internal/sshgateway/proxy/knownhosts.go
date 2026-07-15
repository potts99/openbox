// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/openbox-dev/openbox/internal/sshgateway"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// TOFUHostKeys pins each instance SSH host key on first use and refuses later
// changes. The store contains public host keys only, but is kept owner-writable
// so another local user cannot replace trust records.
type TOFUHostKeys struct {
	path      string
	directory os.FileInfo
	mu        sync.Mutex
}

func NewTOFUHostKeys(path string) (*TOFUHostKeys, error) {
	if path == "" {
		return nil, errors.New("instance known-hosts path is required")
	}
	directoryPath := filepath.Dir(path)
	if err := sshgateway.EnsureSafeStorageDirectory(directoryPath); err != nil {
		return nil, fmt.Errorf("secure instance known-hosts directory: %w", err)
	}
	directory, err := os.Lstat(directoryPath)
	if err != nil {
		return nil, fmt.Errorf("inspect instance known-hosts directory: %w", err)
	}
	return &TOFUHostKeys{path: path, directory: directory}, nil
}

func (s *TOFUHostKeys) Callback(hostname string, remote net.Addr, key ssh.PublicKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	directoryPath := filepath.Dir(s.path)
	if err := sshgateway.EnsureSafeStorageDirectory(directoryPath); err != nil {
		return fmt.Errorf("secure instance known-hosts directory: %w", err)
	}
	directory, err := os.Lstat(directoryPath)
	if err != nil || !os.SameFile(s.directory, directory) {
		return errors.New("instance known-hosts directory changed after initialization")
	}
	info, err := os.Lstat(s.path)
	var existingInfo os.FileInfo
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
			return errors.New("instance known-hosts file must be a regular file not writable by group or others")
		}
		existingInfo = info
		if info.Size() > 0 {
			callback, err := knownhosts.New(s.path)
			if err != nil {
				return fmt.Errorf("load instance host keys: %w", err)
			}
			err = callback(hostname, remote, key)
			if err == nil {
				return nil
			}
			var keyError *knownhosts.KeyError
			if !errors.As(err, &keyError) || len(keyError.Want) != 0 {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect instance known-hosts: %w", err)
	}
	file, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open instance known-hosts: %w", err)
	}
	opened, statErr := file.Stat()
	pathInfo, pathErr := os.Lstat(s.path)
	if statErr != nil || pathErr != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(pathInfo, opened) || (existingInfo != nil && !os.SameFile(existingInfo, opened)) {
		_ = file.Close()
		return errors.New("instance known-hosts changed while opening")
	}
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n"
	if _, err := file.WriteString(line); err != nil {
		_ = file.Close()
		return fmt.Errorf("pin instance host key: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync instance host key: %w", err)
	}
	return file.Close()
}
