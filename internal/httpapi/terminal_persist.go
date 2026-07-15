// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"sync"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// persistentConsole is a named terminal console retained across WebSocket
// detach/reconnect. Ephemeral (unnamed) shells are never stored here.
//
// A single long-lived stdout pump reads the PTY. While a browser is attached,
// bytes are forwarded into an io.Pipe consumed by the WebSocket bridge; while
// detached, bytes are discarded so the guest tmux session can keep producing
// output without blocking.
type persistentConsole struct {
	id          string
	sessionName string
	ownerID     string
	instanceID  string
	console     runtimeapi.ConsoleSession
	attached    bool

	outMu sync.Mutex
	outW  *io.PipeWriter
}

func (p *persistentConsole) startStdoutPump() {
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := p.console.Stdout().Read(buf)
			if n > 0 {
				p.outMu.Lock()
				w := p.outW
				p.outMu.Unlock()
				if w != nil {
					if _, writeErr := w.Write(buf[:n]); writeErr != nil {
						p.detachOutput()
					}
				}
			}
			if err != nil {
				p.detachOutput()
				return
			}
		}
	}()
}

// attachOutput returns a reader that receives subsequent PTY stdout. The caller
// must detachOutput when the WebSocket bridge ends.
func (p *persistentConsole) attachOutput() io.Reader {
	r, w := io.Pipe()
	p.outMu.Lock()
	if p.outW != nil {
		_ = p.outW.Close()
	}
	p.outW = w
	p.outMu.Unlock()
	return r
}

func (p *persistentConsole) detachOutput() {
	p.outMu.Lock()
	defer p.outMu.Unlock()
	if p.outW != nil {
		_ = p.outW.Close()
		p.outW = nil
	}
}

// persistentConsoleStore maps daemon-side session_id and (owner, instance, name)
// onto live ConsoleSession values so named tmux sessions can reattach without
// opening a new PTY.
type persistentConsoleStore struct {
	mu     sync.Mutex
	byID   map[string]*persistentConsole
	byName map[string]*persistentConsole
}

func newPersistentConsoleStore() *persistentConsoleStore {
	return &persistentConsoleStore{
		byID:   make(map[string]*persistentConsole),
		byName: make(map[string]*persistentConsole),
	}
}

func persistentNameKey(ownerID, instanceID, sessionName string) string {
	return ownerID + "\x00" + instanceID + "\x00" + sessionName
}

func newTerminalSessionID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func (s *persistentConsoleStore) put(entry *persistentConsole) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[entry.id] = entry
	s.byName[persistentNameKey(entry.ownerID, entry.instanceID, entry.sessionName)] = entry
}

func (s *persistentConsoleStore) getByID(id string) *persistentConsole {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byID[id]
}

func (s *persistentConsoleStore) getByName(ownerID, instanceID, sessionName string) *persistentConsole {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byName[persistentNameKey(ownerID, instanceID, sessionName)]
}

// claimAttached marks a detached entry attached for exclusive bridging.
// Returns false if missing or already attached.
func (s *persistentConsoleStore) claimAttached(entry *persistentConsole) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.byID[entry.id]
	if current == nil || current != entry || current.attached {
		return false
	}
	current.attached = true
	return true
}

func (s *persistentConsoleStore) markDetached(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.byID[id]; entry != nil {
		entry.attached = false
	}
}

func (s *persistentConsoleStore) remove(id string) *persistentConsole {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.byID[id]
	if entry == nil {
		return nil
	}
	delete(s.byID, id)
	delete(s.byName, persistentNameKey(entry.ownerID, entry.instanceID, entry.sessionName))
	entry.detachOutput()
	return entry
}
