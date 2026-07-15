// SPDX-License-Identifier: AGPL-3.0-only

// Package sshgateway implements the deny-by-default OpenBox SSH boundary.
package sshgateway

import (
	"context"
	"io"
	"net"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"golang.org/x/crypto/ssh"
)

const DefaultAddress = ":2222"

type KeyAuthorizer interface {
	AuthorizeSSHKey(context.Context, string) (domain.OwnerID, bool, error)
}

type CommandDispatcher interface {
	Execute(context.Context, domain.OwnerID, string, io.Reader, io.Writer, io.Writer) int
}

type InstanceTarget struct {
	Name string
	Ref  string
}

// InstanceProxy owns durable start/readiness and the internal SSH credential.
// Neither credential material nor a host-shell primitive crosses this boundary.
type InstanceProxy interface {
	EnsureReady(context.Context, domain.OwnerID, string, io.Writer) (InstanceTarget, error)
	Open(context.Context, InstanceTarget) (RemoteSession, error)
}

type RemoteSession interface {
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Stderr() io.Reader
	RequestPTY(string, int, int, ssh.TerminalModes) error
	WindowChange(int, int) error
	Signal(ssh.Signal) error
	Shell() error
	Start(string) error
	Wait() (int, error)
	Close() error
}

type AuditEvent struct {
	At          time.Time
	RemoteIP    string
	OwnerID     domain.OwnerID
	Fingerprint string
	Command     string
	Target      string
	Outcome     string
}

type Auditor interface {
	Record(context.Context, AuditEvent) error
}

type Config struct {
	Address               string
	HostKeyPath           string
	Keys                  KeyAuthorizer
	Commands              CommandDispatcher
	Instances             InstanceProxy
	Audit                 Auditor
	ReadyTimeout          time.Duration
	AuthTimeout           time.Duration
	OpenTimeout           time.Duration
	AuditTimeout          time.Duration
	AuthWindow            time.Duration
	AuthAttemptsPerIP     int
	AuthAttemptsPerKey    int
	PendingHandshakes     int
	GlobalConnections     int
	ConnectionsPerKey     int
	GlobalSessions        int
	SessionsPerConnection int
	Now                   func() time.Time
	Listen                func(string, string) (net.Listener, error)
}
