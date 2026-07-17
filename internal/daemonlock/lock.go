// SPDX-License-Identifier: AGPL-3.0-only

// Package daemonlock coordinates exclusive host ownership between openboxd and
// offline maintenance commands such as backup restore.
package daemonlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ErrHeld means another process already owns the OpenBox host lock.
var ErrHeld = errors.New("openboxd host lock is held")

// File is an exclusive host lock kept open for the lifetime of the owner.
type File struct {
	file *os.File
}

// PathForDatabase returns the lock path beside the control-plane database.
func PathForDatabase(databasePath string) string {
	return filepath.Join(filepath.Dir(databasePath), "openboxd.lock")
}

// TryAcquire opens path and takes a non-blocking exclusive flock.
func TryAcquire(path string) (*File, error) {
	if path == "" {
		return nil, errors.New("daemon lock path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create daemon lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open daemon lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("acquire daemon lock: %w", err)
	}
	return &File{file: file}, nil
}

// Close releases the flock and closes the lock file.
func (l *File) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	err := l.file.Close()
	l.file = nil
	return err
}
