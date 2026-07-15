// SPDX-License-Identifier: AGPL-3.0-only

// Package proxy starts instances durably and opens their internal SSH service.
package proxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/sshgateway"
	"golang.org/x/crypto/ssh"
)

type Service interface {
	ListInstances(context.Context, domain.OwnerID) ([]domain.Instance, error)
	GetInstance(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error)
	SubmitAction(context.Context, domain.OwnerID, domain.InstanceID, instances.MutationAction, string) (domain.Operation, error)
	GetOperation(context.Context, domain.OwnerID, domain.OperationID) (domain.Operation, error)
}

type AddressResolver interface {
	InstanceSSHAddress(context.Context, string) (string, error)
}

type Options struct {
	PollInterval time.Duration
	DialTimeout  time.Duration
	HostKey      ssh.HostKeyCallback
}

type Proxy struct {
	service    Service
	addresses  AddressResolver
	signer     ssh.Signer
	poll, dial time.Duration
	hostKey    ssh.HostKeyCallback
}

func New(service Service, addresses AddressResolver, signer ssh.Signer, options Options) (*Proxy, error) {
	if service == nil || addresses == nil || signer == nil || options.HostKey == nil {
		return nil, errors.New("instance service, address resolver, internal signer, and host-key verifier are required")
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 250 * time.Millisecond
	}
	if options.DialTimeout <= 0 {
		options.DialTimeout = 10 * time.Second
	}
	return &Proxy{service: service, addresses: addresses, signer: signer, poll: options.PollInterval, dial: options.DialTimeout, hostKey: options.HostKey}, nil
}

func (p *Proxy) EnsureReady(ctx context.Context, owner domain.OwnerID, name string, progress io.Writer) (sshgateway.InstanceTarget, error) {
	values, err := p.service.ListInstances(ctx, owner)
	if err != nil {
		return sshgateway.InstanceTarget{}, err
	}
	var found domain.Instance
	for _, value := range values {
		if value.Name == name {
			found = value
			break
		}
	}
	if found.ID == "" {
		return sshgateway.InstanceTarget{}, &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
	}
	if found.DesiredState == domain.DesiredDeleted || found.ObservedState == domain.ObservedDeleting || found.ObservedState == domain.ObservedDeleted {
		return sshgateway.InstanceTarget{}, errors.New("instance is being deleted")
	}
	var operationID domain.OperationID
	if found.ObservedState == domain.ObservedStopped || found.DesiredState == domain.DesiredStopped {
		key, err := randomKey()
		if err != nil {
			return sshgateway.InstanceTarget{}, err
		}
		operation, err := p.service.SubmitAction(ctx, owner, found.ID, instances.MutationStart, key)
		if err != nil {
			return sshgateway.InstanceTarget{}, err
		}
		operationID = operation.ID
		fmt.Fprintf(progress, "Starting %s (operation %s)...\n", found.Name, operation.ID)
	}
	for {
		current, err := p.service.GetInstance(ctx, owner, found.ID)
		if err != nil {
			return sshgateway.InstanceTarget{}, err
		}
		if current.ObservedState == domain.ObservedError {
			return sshgateway.InstanceTarget{}, fmt.Errorf("instance failed to start: %s", current.ErrorCode)
		}
		operationReady := operationID == ""
		if operationID != "" {
			operation, err := p.service.GetOperation(ctx, owner, operationID)
			if err != nil {
				return sshgateway.InstanceTarget{}, err
			}
			if operation.Status == domain.OperationFailed {
				return sshgateway.InstanceTarget{}, fmt.Errorf("instance start failed: %s", operation.ErrorCode)
			}
			operationReady = operation.Status == domain.OperationSucceeded
		}
		if current.ObservedState == domain.ObservedRunning && operationReady {
			if current.RuntimeRef == "" {
				return sshgateway.InstanceTarget{}, errors.New("running instance has no runtime identity")
			}
			return sshgateway.InstanceTarget{Name: current.Name, Ref: current.RuntimeRef}, nil
		}
		if err := wait(ctx, p.poll); err != nil {
			return sshgateway.InstanceTarget{}, err
		}
	}
}

func (p *Proxy) DialPort(ctx context.Context, owner domain.OwnerID, name string, port uint32) (net.Conn, error) {
	if port < 1 || port > 65535 {
		return nil, errors.New("invalid forward port")
	}
	target, err := p.EnsureReady(ctx, owner, name, io.Discard)
	if err != nil {
		return nil, err
	}
	if target.Ref == "" {
		return nil, errors.New("instance runtime identity is required")
	}
	address, err := p.addresses.InstanceSSHAddress(ctx, target.Ref)
	if err != nil {
		return nil, fmt.Errorf("resolve instance address: %w", err)
	}
	dialer := net.Dialer{Timeout: p.dial}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address, fmt.Sprintf("%d", port)))
	if err != nil {
		return nil, fmt.Errorf("dial instance port: %w", err)
	}
	return connection, nil
}

func (p *Proxy) Open(ctx context.Context, target sshgateway.InstanceTarget) (sshgateway.RemoteSession, error) {
	if target.Ref == "" {
		return nil, errors.New("instance runtime identity is required")
	}
	address, err := p.addresses.InstanceSSHAddress(ctx, target.Ref)
	if err != nil {
		return nil, fmt.Errorf("resolve instance SSH address: %w", err)
	}
	dialer := net.Dialer{Timeout: p.dial}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address, "22"))
	if err != nil {
		return nil, fmt.Errorf("dial instance SSH: %w", err)
	}
	config := &ssh.ClientConfig{User: "root", Auth: []ssh.AuthMethod{ssh.PublicKeys(p.signer)}, HostKeyCallback: p.hostKey, Timeout: p.dial}
	// Verify/pin against the stable runtime identity, not a reusable private IP.
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, target.Ref, config)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("authenticate instance SSH: %w", err)
	}
	client := ssh.NewClient(clientConnection, channels, requests)
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, err
	}
	return &remote{client: client, session: session, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

type remote struct {
	client         *ssh.Client
	session        *ssh.Session
	stdin          io.WriteCloser
	stdout, stderr io.Reader
}

func (r *remote) Stdin() io.WriteCloser { return r.stdin }
func (r *remote) Stdout() io.Reader     { return r.stdout }
func (r *remote) Stderr() io.Reader     { return r.stderr }
func (r *remote) RequestPTY(term string, height, width int, modes ssh.TerminalModes) error {
	return r.session.RequestPty(term, height, width, modes)
}
func (r *remote) WindowChange(height, width int) error { return r.session.WindowChange(height, width) }
func (r *remote) Signal(signal ssh.Signal) error       { return r.session.Signal(signal) }
func (r *remote) Shell() error                         { return r.session.Shell() }
func (r *remote) Start(command string) error           { return r.session.Start(command) }
func (r *remote) Wait() (int, error) {
	err := r.session.Wait()
	if err == nil {
		return 0, nil
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return exit.ExitStatus(), nil
	}
	return 255, err
}
func (r *remote) Close() error { return errors.Join(r.session.Close(), r.client.Close()) }

func randomKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "ssh-enter-" + hex.EncodeToString(raw), nil
}
func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
