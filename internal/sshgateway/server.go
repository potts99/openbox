// SPDX-License-Identifier: AGPL-3.0-only

package sshgateway

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"golang.org/x/crypto/ssh"
)

const (
	defaultReadyTimeout     = 2 * time.Minute
	defaultAuthTimeout      = 5 * time.Second
	defaultOpenTimeout      = 15 * time.Second
	defaultAuditTimeout     = 2 * time.Second
	defaultAuthWindow       = time.Minute
	defaultHandshakeTimeout = 15 * time.Second
	maximumLimiterEntries   = 4096
)

type Server struct {
	config      Config
	sshConfig   *ssh.ServerConfig
	attempts    *attemptWindow
	connections *connectionLimits
	handshakes  chan struct{}
	sessions    chan struct{}
}

func New(config Config) (*Server, error) {
	if config.Keys == nil || config.Audit == nil {
		return nil, errors.New("SSH key authorizer and auditor are required")
	}
	if config.Address == "" {
		config.Address = DefaultAddress
	}
	if config.ReadyTimeout <= 0 {
		config.ReadyTimeout = defaultReadyTimeout
	}
	if config.AuthTimeout <= 0 {
		config.AuthTimeout = defaultAuthTimeout
	}
	if config.OpenTimeout <= 0 {
		config.OpenTimeout = defaultOpenTimeout
	}
	if config.AuditTimeout <= 0 {
		config.AuditTimeout = defaultAuditTimeout
	}
	if config.AuthWindow <= 0 {
		config.AuthWindow = defaultAuthWindow
	}
	if config.AuthAttemptsPerIP <= 0 {
		config.AuthAttemptsPerIP = 20
	}
	if config.AuthAttemptsPerKey <= 0 {
		config.AuthAttemptsPerKey = 10
	}
	if config.GlobalConnections <= 0 {
		config.GlobalConnections = 128
	}
	if config.ConnectionsPerKey <= 0 {
		config.ConnectionsPerKey = 8
	}
	if config.PendingHandshakes <= 0 {
		config.PendingHandshakes = 64
	}
	if config.GlobalSessions <= 0 {
		config.GlobalSessions = 256
	}
	if config.SessionsPerConnection <= 0 {
		config.SessionsPerConnection = 4
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.Listen == nil {
		config.Listen = net.Listen
	}
	signer, err := LoadOrCreateHostKey(config.HostKeyPath)
	if err != nil {
		return nil, err
	}

	server := &Server{
		config:      config,
		attempts:    &attemptWindow{now: config.Now, window: config.AuthWindow, limitIP: config.AuthAttemptsPerIP, limitKey: config.AuthAttemptsPerKey, maxEntries: maximumLimiterEntries, items: make(map[string][]time.Time)},
		connections: &connectionLimits{global: config.GlobalConnections, perKey: config.ConnectionsPerKey, keys: make(map[string]int)},
		handshakes:  make(chan struct{}, config.PendingHandshakes),
		sessions:    make(chan struct{}, config.GlobalSessions),
	}
	server.sshConfig = &ssh.ServerConfig{
		NoClientAuth:  false,
		MaxAuthTries:  3,
		ServerVersion: "SSH-2.0-OpenBox",
	}
	server.sshConfig.AddHostKey(signer)
	return server, nil
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	listener, err := s.config.Listen("tcp", s.config.Address)
	if err != nil {
		return fmt.Errorf("listen for SSH gateway: %w", err)
	}
	return s.Serve(ctx, listener)
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	if listener == nil {
		return errors.New("SSH listener is required")
	}
	defer listener.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-done:
		}
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept SSH connection: %w", err)
		}
		select {
		case s.handshakes <- struct{}{}:
			go s.handleConnection(ctx, connection)
		default:
			s.record(context.Background(), AuditEvent{At: s.config.Now(), RemoteIP: remoteIP(connection.RemoteAddr()), Outcome: "handshake_limited"})
			_ = connection.Close()
		}
	}
}

func (s *Server) authorize(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	fingerprint := ssh.FingerprintSHA256(key)
	ip := remoteIP(metadata.RemoteAddr())
	kind, target, err := parseUsername(metadata.User())
	if err != nil {
		s.record(context.Background(), AuditEvent{At: s.config.Now(), RemoteIP: ip, Fingerprint: fingerprint, Outcome: "authentication_denied"})
		return nil, err
	}
	if !s.attempts.allow(ip, fingerprint) {
		s.record(context.Background(), AuditEvent{At: s.config.Now(), RemoteIP: ip, Fingerprint: fingerprint, Target: target, Outcome: "rate_limited"})
		return nil, errors.New("SSH authentication rate limited")
	}
	authContext, cancel := context.WithTimeout(context.Background(), s.config.AuthTimeout)
	owner, allowed, err := s.config.Keys.AuthorizeSSHKey(authContext, fingerprint)
	cancel()
	if err != nil || !allowed || owner == "" {
		s.record(context.Background(), AuditEvent{At: s.config.Now(), RemoteIP: ip, Fingerprint: fingerprint, Target: target, Outcome: "authentication_denied"})
		if err != nil {
			return nil, errors.New("SSH key authorization unavailable")
		}
		return nil, errors.New("unregistered SSH key")
	}
	return &ssh.Permissions{Extensions: map[string]string{
		"openbox-owner": string(owner), "openbox-fingerprint": fingerprint,
		"openbox-kind": fmt.Sprint(kind), "openbox-target": target,
	}}, nil
}

func (s *Server) handleConnection(parent context.Context, raw net.Conn) {
	defer raw.Close()
	handshakePending := true
	defer func() {
		if handshakePending {
			<-s.handshakes
		}
	}()
	_ = raw.SetDeadline(time.Now().Add(defaultHandshakeTimeout))
	configuration := *s.sshConfig
	var reserved string
	configuration.PublicKeyCallback = func(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		permissions, err := s.authorize(metadata, key)
		if err != nil {
			return nil, err
		}
		fingerprint := permissions.Extensions["openbox-fingerprint"]
		if reserved == "" {
			if !s.connections.acquire(fingerprint) {
				s.record(context.Background(), AuditEvent{At: s.config.Now(), RemoteIP: remoteIP(metadata.RemoteAddr()), OwnerID: domain.OwnerID(permissions.Extensions["openbox-owner"]), Fingerprint: fingerprint, Target: permissions.Extensions["openbox-target"], Outcome: "connection_limited"})
				return nil, errors.New("SSH connection limit reached")
			}
			reserved = fingerprint
		} else if reserved != fingerprint {
			return nil, errors.New("SSH authentication key changed during handshake")
		}
		return permissions, nil
	}
	connection, channels, requests, err := ssh.NewServerConn(raw, &configuration)
	<-s.handshakes
	handshakePending = false
	if reserved != "" {
		defer s.connections.release(reserved)
	}
	if err != nil {
		return
	}
	_ = raw.SetDeadline(time.Time{})
	defer connection.Close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	connectionDone := make(chan struct{})
	defer close(connectionDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-connectionDone:
		}
	}()
	connectionEvent := AuditEvent{RemoteIP: remoteIP(connection.RemoteAddr()), OwnerID: domain.OwnerID(connection.Permissions.Extensions["openbox-owner"]), Fingerprint: connection.Permissions.Extensions["openbox-fingerprint"], Target: connection.Permissions.Extensions["openbox-target"]}
	go s.rejectGlobalRequests(ctx, requests, connectionEvent)
	connectionSessions := make(chan struct{}, s.config.SessionsPerConnection)
	for channel := range channels {
		if channel.ChannelType() != "session" {
			event := connectionEvent
			event.Command = channel.ChannelType()
			event.Outcome = "refused"
			s.record(ctx, event)
			_ = channel.Reject(ssh.UnknownChannelType, "OpenBox only supports session channels")
			continue
		}
		select {
		case connectionSessions <- struct{}{}:
		default:
			_ = channel.Reject(ssh.ResourceShortage, "per-connection SSH session limit reached")
			continue
		}
		select {
		case s.sessions <- struct{}{}:
		default:
			<-connectionSessions
			_ = channel.Reject(ssh.ResourceShortage, "global SSH session limit reached")
			continue
		}
		accepted, sessionRequests, err := channel.Accept()
		if err != nil {
			<-s.sessions
			<-connectionSessions
			continue
		}
		go func() {
			defer func() { <-s.sessions; <-connectionSessions }()
			s.handleSession(ctx, connection, accepted, sessionRequests)
		}()
	}
}

func (s *Server) rejectGlobalRequests(ctx context.Context, requests <-chan *ssh.Request, base AuditEvent) {
	for request := range requests {
		event := base
		event.Command = request.Type
		event.Outcome = "refused"
		s.record(ctx, event)
		if request.WantReply {
			_ = request.Reply(false, nil)
		}
	}
}

func (s *Server) handleSession(ctx context.Context, connection *ssh.ServerConn, channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	kind, target, err := parseUsername(connection.User())
	if err != nil {
		return
	}
	owner := domain.OwnerID(connection.Permissions.Extensions["openbox-owner"])
	fingerprint := connection.Permissions.Extensions["openbox-fingerprint"]
	base := AuditEvent{At: s.config.Now(), RemoteIP: remoteIP(connection.RemoteAddr()), OwnerID: owner, Fingerprint: fingerprint, Target: target}
	if kind == sessionControl {
		s.handleControl(ctx, owner, base, channel, requests)
		return
	}
	s.handleInstance(ctx, owner, target, base, channel, requests)
}

func (s *Server) handleControl(ctx context.Context, owner domain.OwnerID, event AuditEvent, channel ssh.Channel, requests <-chan *ssh.Request) {
	for request := range requests {
		if request.Type != "exec" || s.config.Commands == nil {
			rejected := event
			rejected.Command = request.Type
			rejected.Outcome = "refused"
			s.record(ctx, rejected)
			if request.WantReply {
				_ = request.Reply(false, nil)
			}
			continue
		}
		var payload struct{ Command string }
		if ssh.Unmarshal(request.Payload, &payload) != nil || strings.TrimSpace(payload.Command) == "" {
			rejected := event
			rejected.Command = "exec"
			rejected.Outcome = "refused"
			s.record(ctx, rejected)
			if request.WantReply {
				_ = request.Reply(false, nil)
			}
			continue
		}
		_ = request.Reply(true, nil)
		event.Command = auditCommand(payload.Command)
		code := s.config.Commands.Execute(ctx, owner, payload.Command, channel, channel, channel.Stderr())
		outcome := "success"
		if code != 0 {
			outcome = "failed"
		}
		event.Outcome = outcome
		s.record(ctx, event)
		sendExitStatus(channel, code)
		return
	}
}

type exitResult struct {
	code int
	err  error
}

func (s *Server) handleInstance(ctx context.Context, owner domain.OwnerID, name string, event AuditEvent, channel ssh.Channel, requests <-chan *ssh.Request) {
	if s.config.Instances == nil {
		event.Outcome = "unavailable"
		s.record(ctx, event)
		return
	}
	// Reject unsupported requests before starting a stopped instance or opening
	// an internal connection. A subsystem probe must have no lifecycle effects.
	var first *ssh.Request

requestLoop:
	for request := range requests {
		switch request.Type {
		case "pty-req", "shell":
			first = request
		case "exec":
			var payload struct{ Command string }
			command := ""
			if ssh.Unmarshal(request.Payload, &payload) == nil {
				command = auditCommand(payload.Command)
			}
			if command == "" || isSCPCommand(payload.Command) {
				if isSCPCommand(payload.Command) {
					event.Command = "scp"
					event.Outcome = "refused"
					s.record(ctx, event)
				}
				if request.WantReply {
					_ = request.Reply(false, nil)
				}
				continue
			}
			first = request
		default:
			rejected := event
			rejected.Command = request.Type
			rejected.Outcome = "refused"
			s.record(ctx, rejected)
			if request.WantReply {
				_ = request.Reply(false, nil)
			}
			continue
		}
		break requestLoop
	}
	if first == nil {
		event.Outcome = "disconnected"
		s.record(ctx, event)
		return
	}

	readyCtx, cancelReady := context.WithTimeout(ctx, s.config.ReadyTimeout)
	target, err := s.config.Instances.EnsureReady(readyCtx, owner, name, channel.Stderr())
	cancelReady()
	if err != nil {
		event.Outcome = "start_failed"
		s.record(ctx, event)
		if first.WantReply {
			_ = first.Reply(false, nil)
		}
		return
	}
	openCtx, cancelOpen := context.WithTimeout(ctx, s.config.OpenTimeout)
	remote, err := s.config.Instances.Open(openCtx, target)
	cancelOpen()
	if err != nil {
		event.Outcome = "proxy_failed"
		s.record(ctx, event)
		if first.WantReply {
			_ = first.Reply(false, nil)
		}
		return
	}
	defer remote.Close()
	stdin := remote.Stdin()
	defer stdin.Close()
	go func() { _, _ = io.Copy(stdin, channel); _ = stdin.Close() }()
	var outputCopies sync.WaitGroup
	outputCopies.Add(2)
	go func() { defer outputCopies.Done(); _, _ = io.Copy(channel, remote.Stdout()) }()
	go func() { defer outputCopies.Done(); _, _ = io.Copy(channel.Stderr(), remote.Stderr()) }()

	started := false
	done := make(chan exitResult, 1)
	accepted, command, start := handleInstanceRequest(remote, first, false)
	if first.WantReply {
		_ = first.Reply(accepted, nil)
	}
	if !accepted {
		event.Outcome = "request_failed"
		s.record(ctx, event)
		return
	}
	if start {
		started = true
		event.Command = command
		go func() {
			code, waitErr := remote.Wait()
			outputCopies.Wait()
			done <- exitResult{code: code, err: waitErr}
		}()
	}
	for {
		select {
		case request, ok := <-requests:
			if !ok {
				if !started {
					event.Outcome = "disconnected"
					s.record(ctx, event)
				}
				return
			}
			accepted, command, start := handleInstanceRequest(remote, request, started)
			if request.WantReply {
				_ = request.Reply(accepted, nil)
			}
			if start && accepted {
				started = true
				event.Command = command
				go func() {
					code, waitErr := remote.Wait()
					outputCopies.Wait()
					done <- exitResult{code: code, err: waitErr}
				}()
			}
		case result := <-done:
			outcome := "success"
			if result.err != nil || result.code != 0 {
				outcome = "failed"
			}
			event.Outcome = outcome
			s.record(ctx, event)
			sendExitStatus(channel, result.code)
			return
		case <-ctx.Done():
			event.Outcome = "canceled"
			s.record(context.Background(), event)
			return
		}
	}
}

func handleInstanceRequest(remote RemoteSession, request *ssh.Request, started bool) (bool, string, bool) {
	switch request.Type {
	case "pty-req":
		if started {
			return false, "", false
		}
		term, columns, rows, modes, err := parsePTY(request.Payload)
		if err != nil || remote.RequestPTY(term, int(rows), int(columns), modes) != nil {
			return false, "", false
		}
		return true, "", false
	case "window-change":
		var dimensions struct{ Columns, Rows, Width, Height uint32 }
		if ssh.Unmarshal(request.Payload, &dimensions) != nil || remote.WindowChange(int(dimensions.Rows), int(dimensions.Columns)) != nil {
			return false, "", false
		}
		return true, "", false
	case "signal":
		var signal struct{ Name string }
		if ssh.Unmarshal(request.Payload, &signal) != nil || remote.Signal(ssh.Signal(signal.Name)) != nil {
			return false, "", false
		}
		return true, "", false
	case "shell":
		if started || len(request.Payload) != 0 || remote.Shell() != nil {
			return false, "", false
		}
		return true, "shell", true
	case "exec":
		if started {
			return false, "", false
		}
		var payload struct{ Command string }
		if ssh.Unmarshal(request.Payload, &payload) != nil || strings.TrimSpace(payload.Command) == "" || remote.Start(payload.Command) != nil {
			return false, "", false
		}
		return true, auditCommand(payload.Command), true
	default:
		// env, subsystem, auth-agent, X11 and every extension are refused.
		return false, "", false
	}
}

func parsePTY(payload []byte) (string, uint32, uint32, ssh.TerminalModes, error) {
	var request struct {
		Term                         string
		Columns, Rows, Width, Height uint32
		Modes                        string
	}
	if ssh.Unmarshal(payload, &request) != nil {
		return "", 0, 0, nil, errors.New("malformed PTY request")
	}
	modes := make(ssh.TerminalModes)
	raw := []byte(request.Modes)
	for len(raw) > 0 {
		opcode := raw[0]
		raw = raw[1:]
		if opcode == 0 {
			return request.Term, request.Columns, request.Rows, modes, nil
		}
		if len(raw) < 4 {
			return "", 0, 0, nil, errors.New("malformed terminal modes")
		}
		modes[opcode] = binary.BigEndian.Uint32(raw[:4])
		raw = raw[4:]
	}
	return "", 0, 0, nil, errors.New("unterminated terminal modes")
}

func sendExitStatus(channel ssh.Channel, code int) {
	if code < 0 {
		code = 255
	}
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
}

func isSCPCommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	executable := strings.TrimSuffix(fields[0], "/")
	if slash := strings.LastIndexByte(executable, '/'); slash >= 0 {
		executable = executable[slash+1:]
	}
	return executable == "scp"
}

func (s *Server) record(ctx context.Context, event AuditEvent) {
	event.At = s.config.Now()
	auditContext, cancel := context.WithTimeout(ctx, s.config.AuditTimeout)
	defer cancel()
	_ = s.config.Audit.Record(auditContext, event)
}

func remoteIP(address net.Addr) string {
	if address == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(address.String())
	if err == nil {
		return host
	}
	return address.String()
}
