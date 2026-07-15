// SPDX-License-Identifier: AGPL-3.0-only

package sshgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"golang.org/x/crypto/ssh"
)

func TestParseUsername(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		username string
		kind     sessionKind
		target   string
	}{
		{"openbox", sessionControl, ""},
		{"work", sessionInstance, "work"},
		{"work-2.openbox", sessionInstance, "work-2"},
	} {
		test := test
		t.Run(test.username, func(t *testing.T) {
			kind, target, err := parseUsername(test.username)
			if err != nil || kind != test.kind || target != test.target {
				t.Fatalf("got (%v,%q,%v)", kind, target, err)
			}
		})
	}
	for _, username := range []string{"", "OpenBox", "bad_name", ".openbox", "work.openbox.openbox", "-bad"} {
		if _, _, err := parseUsername(username); err == nil {
			t.Errorf("accepted malformed username %q", username)
		}
	}
}

func FuzzParseUsername(f *testing.F) {
	for _, seed := range []string{"openbox", "dev", "dev.openbox", "", "a/b", strings.Repeat("a", 64)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, username string) {
		kind, target, err := parseUsername(username)
		if err != nil {
			return
		}
		if kind == sessionControl && (username != "openbox" || target != "") {
			t.Fatalf("invalid control parse")
		}
		if kind == sessionInstance && domain.ValidateInstanceName(target) != nil {
			t.Fatalf("invalid instance target %q", target)
		}
	})
}

func TestHostKeyIsStableAndOwnerOnly(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "keys", "gateway")
	first, err := LoadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateHostKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.PublicKey().Marshal(), second.PublicKey().Marshal()) {
		t.Fatal("host identity changed")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %o", info.Mode().Perm())
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateHostKey(path); err == nil {
		t.Fatal("accepted group-readable host key")
	}
}

func TestHostKeyRefusesSymbolicLink(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "gateway")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateHostKey(link); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestHostKeyRefusesWritableDirectory(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "writable")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateHostKey(filepath.Join(directory, "gateway")); err == nil || !strings.Contains(err.Error(), "group- or other-writable") {
		t.Fatalf("writable directory error = %v", err)
	}
}

func TestHostKeyRefusesSymlinkDirectory(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateHostKey(filepath.Join(link, "gateway")); err == nil || !strings.Contains(err.Error(), "directory") || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink directory error = %v", err)
	}
}

func TestControlCommandAndDenyByDefault(t *testing.T) {
	signer := newSigner(t)
	audit := &memoryAudit{}
	dispatch := &fakeDispatcher{}
	client, stop := startTestServer(t, Config{
		Keys:     keyAuthorizer{fingerprint: ssh.FingerprintSHA256(signer.PublicKey())},
		Commands: dispatch, Audit: audit,
	}, signer, "openbox")
	defer stop()

	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Run("new machine --token super-secret"); err != nil {
		t.Fatal(err)
	}
	if dispatch.command != "new machine --token super-secret" {
		t.Fatalf("command = %q", dispatch.command)
	}
	events := audit.snapshot()
	if len(events) != 1 || events[0].Command != "new" || strings.Contains(strings.Join([]string{events[0].Command, events[0].Target}, " "), "super-secret") {
		t.Fatalf("unsafe audit event: %#v", events)
	}

	sftp, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := sftp.RequestSubsystem("sftp"); err == nil {
		t.Fatal("SFTP subsystem accepted")
	}
	_ = sftp.Close()
	if _, err := client.Dial("tcp", "127.0.0.1:80"); err == nil {
		t.Fatal("TCP forwarding accepted")
	}
	if _, _, err := client.OpenChannel("auth-agent@openssh.com", nil); err == nil {
		t.Fatal("agent channel accepted")
	}
}

func TestInstanceInteractiveProxy(t *testing.T) {
	signer := newSigner(t)
	audit := &memoryAudit{}
	remote := newFakeRemote()
	remote.waitGate = make(chan struct{})
	proxy := &fakeInstanceProxy{remote: remote}
	client, stop := startTestServer(t, Config{
		Keys:      keyAuthorizer{fingerprint: ssh.FingerprintSHA256(signer.PublicKey())},
		Instances: proxy, Audit: audit, ReadyTimeout: time.Second,
	}, signer, "dev.openbox")
	defer stop()

	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.RequestPty("xterm", 40, 100, ssh.TerminalModes{ssh.ECHO: 0}); err != nil {
		t.Fatal(err)
	}
	if err := session.WindowChange(50, 120); err != nil {
		t.Fatal(err)
	}
	if err := session.Start("printf hello"); err != nil {
		t.Fatal(err)
	}
	if err := session.Signal(ssh.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-remote.signalSeen:
	case <-time.After(time.Second):
		t.Fatal("signal was not forwarded")
	}
	close(remote.waitGate)
	err = session.Wait()
	var exit *ssh.ExitError
	if !errors.As(err, &exit) || exit.ExitStatus() != 7 {
		t.Fatalf("expected exit 7, got %v", err)
	}
	if stdout.String() != "instance output\n" || stderr.String() != "starting\ninstance warning\n" {
		t.Fatalf("output = %q / %q", stdout.String(), stderr.String())
	}
	proxy.mu.Lock()
	name := proxy.name
	proxy.mu.Unlock()
	remote.mu.Lock()
	command, term, height, width, signal := remote.command, remote.term, remote.height, remote.width, remote.signal
	remote.mu.Unlock()
	if name != "dev" || command != "printf hello" || term != "xterm" || height != 50 || width != 120 || signal != ssh.SIGTERM {
		t.Fatalf("proxy state: name=%q command=%q term=%q size=%dx%d signal=%q", name, command, term, height, width, signal)
	}
	events := audit.snapshot()
	if len(events) != 1 || events[0].Target != "dev" || events[0].Command != "printf" || events[0].Outcome != "failed" {
		t.Fatalf("audit = %#v", events)
	}
}

func TestInstanceRefusesSCPWithoutLifecycleEffect(t *testing.T) {
	signer := newSigner(t)
	audit := &memoryAudit{}
	proxy := &fakeInstanceProxy{remote: newFakeRemote()}
	client, stop := startTestServer(t, Config{
		Keys:      keyAuthorizer{fingerprint: ssh.FingerprintSHA256(signer.PublicKey())},
		Instances: proxy, Audit: audit,
	}, signer, "dev")
	defer stop()
	for _, run := range []func(*ssh.Session) error{
		func(session *ssh.Session) error { return session.RequestSubsystem("sftp") },
		func(session *ssh.Session) error { return session.Run("/usr/bin/scp -t /tmp/file") },
	} {
		session, err := client.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		if err := run(session); err == nil {
			t.Fatal("unsupported file transfer accepted")
		}
		_ = session.Close()
	}
	proxy.mu.Lock()
	calls := proxy.calls
	proxy.mu.Unlock()
	if calls != 0 {
		t.Fatalf("file transfer started instance %d times", calls)
	}
	if !containsOutcome(audit.snapshot(), "refused") {
		t.Fatalf("SCP refusal was not audited: %#v", audit.snapshot())
	}
}

func TestInstanceReadyWaitIsBounded(t *testing.T) {
	signer := newSigner(t)
	audit := &memoryAudit{}
	client, stop := startTestServer(t, Config{
		Keys:      keyAuthorizer{fingerprint: ssh.FingerprintSHA256(signer.PublicKey())},
		Instances: &blockingInstanceProxy{}, Audit: audit, ReadyTimeout: 30 * time.Millisecond,
	}, signer, "dev")
	defer stop()
	started := time.Now()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Run("true"); err == nil {
		t.Fatal("timed-out start reported success")
	}
	if time.Since(started) > time.Second {
		t.Fatal("readiness timeout was not bounded")
	}
	if !containsOutcome(audit.snapshot(), "start_failed") {
		t.Fatalf("timeout not audited: %#v", audit.snapshot())
	}
}

func TestInstanceOpenIsBounded(t *testing.T) {
	signer := newSigner(t)
	audit := &memoryAudit{}
	client, stop := startTestServer(t, Config{
		Keys:      keyAuthorizer{fingerprint: ssh.FingerprintSHA256(signer.PublicKey())},
		Instances: &blockingOpenProxy{}, Audit: audit, OpenTimeout: 30 * time.Millisecond,
	}, signer, "dev")
	defer stop()
	started := time.Now()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Run("true"); err == nil {
		t.Fatal("timed-out open reported success")
	}
	if time.Since(started) > time.Second {
		t.Fatal("open timeout was not bounded")
	}
	if !containsOutcome(audit.snapshot(), "proxy_failed") {
		t.Fatalf("open timeout not audited: %#v", audit.snapshot())
	}
}

func TestUnknownKeyRateLimitAndConnectionLimit(t *testing.T) {
	allowed := newSigner(t)
	unknown := newSigner(t)
	audit := &memoryAudit{}
	server, listener, cancel := newListeningServer(t, Config{
		Keys: keyAuthorizer{fingerprint: ssh.FingerprintSHA256(allowed.PublicKey())}, Audit: audit,
		AuthAttemptsPerIP: 2, AuthAttemptsPerKey: 2, ConnectionsPerKey: 1,
	})
	defer cancel()
	_ = server
	clientConfig := sshClientConfig("openbox", unknown)
	for range 3 {
		connection, err := ssh.Dial("tcp", listener.Addr().String(), clientConfig)
		if err == nil {
			connection.Close()
			t.Fatal("unknown key accepted")
		}
	}
	events := audit.snapshot()
	if !containsOutcome(events, "rate_limited") {
		t.Fatalf("rate limit was not audited: %#v", events)
	}

	// A separate server avoids the IP limiter consumed by the unknown-key attempts.
	cancel()
	_, listener, cancel = newListeningServer(t, Config{
		Keys: keyAuthorizer{fingerprint: ssh.FingerprintSHA256(allowed.PublicKey())}, Audit: audit, ConnectionsPerKey: 1,
	})
	defer cancel()
	first, err := ssh.Dial("tcp", listener.Addr().String(), sshClientConfig("openbox", allowed))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := ssh.Dial("tcp", listener.Addr().String(), sshClientConfig("openbox", allowed)); err == nil {
		second.Close()
		t.Fatal("per-key connection limit not enforced")
	}
}

func TestKeyAuthorizationIsBounded(t *testing.T) {
	signer := newSigner(t)
	_, listener, cancel := newListeningServer(t, Config{Keys: blockingAuthorizer{}, Audit: &memoryAudit{}, AuthTimeout: 30 * time.Millisecond})
	defer cancel()
	started := time.Now()
	if connection, err := ssh.Dial("tcp", listener.Addr().String(), sshClientConfig("openbox", signer)); err == nil {
		connection.Close()
		t.Fatal("timed-out authorization succeeded")
	}
	if time.Since(started) > time.Second {
		t.Fatal("key authorization timeout was not bounded")
	}
}

func TestPreAuthHandshakeLimit(t *testing.T) {
	audit := &memoryAudit{}
	_, listener, cancel := newListeningServer(t, Config{Keys: keyAuthorizer{}, Audit: audit, PendingHandshakes: 1})
	defer cancel()
	first, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 1)
	if _, err := second.Read(buffer); err == nil {
		t.Fatal("excess pre-auth connection remained open")
	}
	deadline := time.Now().Add(time.Second)
	for !containsOutcome(audit.snapshot(), "handshake_limited") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !containsOutcome(audit.snapshot(), "handshake_limited") {
		t.Fatalf("handshake limit not audited: %#v", audit.snapshot())
	}
}

func TestSessionLimitsPerConnectionAndGlobally(t *testing.T) {
	signer := newSigner(t)
	_, listener, cancel := newListeningServer(t, Config{
		Keys: keyAuthorizer{fingerprint: ssh.FingerprintSHA256(signer.PublicKey())}, Audit: &memoryAudit{},
		SessionsPerConnection: 1, GlobalSessions: 1, PendingHandshakes: 1,
	})
	defer cancel()
	firstClient, err := ssh.Dial("tcp", listener.Addr().String(), sshClientConfig("openbox", signer))
	if err != nil {
		t.Fatal(err)
	}
	defer firstClient.Close()
	firstSession, err := firstClient.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer firstSession.Close()
	if extra, err := firstClient.NewSession(); err == nil {
		extra.Close()
		t.Fatal("per-connection session limit not enforced")
	}
	secondClient, err := ssh.Dial("tcp", listener.Addr().String(), sshClientConfig("openbox", signer))
	if err != nil {
		t.Fatal(err)
	}
	defer secondClient.Close()
	if extra, err := secondClient.NewSession(); err == nil {
		extra.Close()
		t.Fatal("global session limit not enforced")
	}
}

func TestFailedHandshakeReleasesKeyConnectionSlot(t *testing.T) {
	valid := newSigner(t)
	authorizer := &countingAuthorizer{fingerprint: ssh.FingerprintSHA256(valid.PublicKey())}
	server, listener, cancel := newListeningServer(t, Config{
		Keys: authorizer, Audit: &memoryAudit{}, ConnectionsPerKey: 1,
	})
	defer cancel()
	bad := signingFailure{Signer: valid}
	connection, err := ssh.Dial("tcp", listener.Addr().String(), sshClientConfig("openbox", bad))
	if err == nil {
		connection.Close()
		t.Fatal("signature failure unexpectedly authenticated")
	}
	authorizer.mu.Lock()
	authorizationCalls := authorizer.calls
	authorizer.mu.Unlock()
	if authorizationCalls == 0 {
		t.Fatal("test did not reach the server key callback")
	}
	deadline := time.Now().Add(time.Second)
	for {
		server.connections.mu.Lock()
		active := server.connections.active
		server.connections.mu.Unlock()
		if active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed handshake leaked %d connection slots", active)
		}
		time.Sleep(time.Millisecond)
	}
	good, err := ssh.Dial("tcp", listener.Addr().String(), sshClientConfig("openbox", valid))
	if err != nil {
		t.Fatalf("released slot could not be reused: %v", err)
	}
	good.Close()
}

func TestAuthLimiterMemoryIsBounded(t *testing.T) {
	now := time.Now()
	window := &attemptWindow{now: func() time.Time { return now }, window: time.Minute, limitIP: 10, limitKey: 10, maxEntries: 64, items: make(map[string][]time.Time)}
	for index := range 1000 {
		window.allow(fmt.Sprintf("192.0.2.%d", index), fmt.Sprintf("key-%d", index))
	}
	if len(window.items) > window.maxEntries {
		t.Fatalf("limiter retained %d entries, max %d", len(window.items), window.maxEntries)
	}
}

func TestPortConflictFailsWithoutDisturbingExistingListener(t *testing.T) {
	t.Parallel()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	server, err := New(Config{Address: occupied.Addr().String(), HostKeyPath: filepath.Join(t.TempDir(), "host"), Keys: keyAuthorizer{}, Audit: &memoryAudit{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.ListenAndServe(context.Background()); err == nil {
		t.Fatal("port conflict did not fail")
	}
	probe, err := net.DialTimeout("tcp", occupied.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("existing listener was disturbed: %v", err)
	}
	probe.Close()
}

type keyAuthorizer struct{ fingerprint string }

func (a keyAuthorizer) AuthorizeSSHKey(_ context.Context, fingerprint string) (domain.OwnerID, bool, error) {
	return "owner-local", fingerprint == a.fingerprint, nil
}

type blockingAuthorizer struct{}

func (blockingAuthorizer) AuthorizeSSHKey(ctx context.Context, _ string) (domain.OwnerID, bool, error) {
	<-ctx.Done()
	return "", false, ctx.Err()
}

type countingAuthorizer struct {
	mu          sync.Mutex
	fingerprint string
	calls       int
}

func (a *countingAuthorizer) AuthorizeSSHKey(_ context.Context, fingerprint string) (domain.OwnerID, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return "owner-local", fingerprint == a.fingerprint, nil
}

type signingFailure struct{ ssh.Signer }

func (s signingFailure) Sign(io.Reader, []byte) (*ssh.Signature, error) {
	return nil, errors.New("signing failed")
}

type fakeDispatcher struct{ command string }

func (d *fakeDispatcher) Execute(_ context.Context, _ domain.OwnerID, command string, _ io.Reader, _, _ io.Writer) int {
	d.command = command
	return 0
}

type memoryAudit struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (a *memoryAudit) Record(_ context.Context, event AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, event)
	return nil
}
func (a *memoryAudit) snapshot() []AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]AuditEvent(nil), a.events...)
}
func containsOutcome(events []AuditEvent, outcome string) bool {
	for _, event := range events {
		if event.Outcome == outcome {
			return true
		}
	}
	return false
}

type fakeInstanceProxy struct {
	mu     sync.Mutex
	name   string
	calls  int
	remote *fakeRemote
}

func (p *fakeInstanceProxy) EnsureReady(_ context.Context, _ domain.OwnerID, name string, progress io.Writer) (InstanceTarget, error) {
	p.mu.Lock()
	p.name = name
	p.calls++
	p.mu.Unlock()
	_, _ = io.WriteString(progress, "starting\n")
	return InstanceTarget{Name: name, Ref: "runtime-" + name}, nil
}
func (p *fakeInstanceProxy) Open(context.Context, InstanceTarget) (RemoteSession, error) {
	return p.remote, nil
}

type blockingInstanceProxy struct{}

func (*blockingInstanceProxy) EnsureReady(ctx context.Context, _ domain.OwnerID, _ string, _ io.Writer) (InstanceTarget, error) {
	<-ctx.Done()
	return InstanceTarget{}, ctx.Err()
}

type blockingOpenProxy struct{}

func (*blockingOpenProxy) EnsureReady(_ context.Context, _ domain.OwnerID, name string, _ io.Writer) (InstanceTarget, error) {
	return InstanceTarget{Name: name, Ref: name}, nil
}
func (*blockingOpenProxy) Open(ctx context.Context, _ InstanceTarget) (RemoteSession, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*blockingInstanceProxy) Open(context.Context, InstanceTarget) (RemoteSession, error) {
	return nil, errors.New("unexpected open")
}

type fakeRemote struct {
	mu            sync.Mutex
	input         *io.PipeWriter
	output        *io.PipeReader
	outputWriter  *io.PipeWriter
	errors        *io.PipeReader
	errorWriter   *io.PipeWriter
	command, term string
	signal        ssh.Signal
	height, width int
	waitGate      chan struct{}
	signalSeen    chan struct{}
}

func newFakeRemote() *fakeRemote {
	output, outputWriter := io.Pipe()
	errors, errorWriter := io.Pipe()
	_, input := io.Pipe()
	return &fakeRemote{input: input, output: output, outputWriter: outputWriter, errors: errors, errorWriter: errorWriter, signalSeen: make(chan struct{})}
}
func (r *fakeRemote) Stdin() io.WriteCloser { return r.input }
func (r *fakeRemote) Stdout() io.Reader     { return r.output }
func (r *fakeRemote) Stderr() io.Reader     { return r.errors }
func (r *fakeRemote) RequestPTY(term string, height, width int, _ ssh.TerminalModes) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.term = term
	r.height = height
	r.width = width
	return nil
}
func (r *fakeRemote) WindowChange(height, width int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.height = height
	r.width = width
	return nil
}
func (r *fakeRemote) Signal(signal ssh.Signal) error {
	r.mu.Lock()
	r.signal = signal
	r.mu.Unlock()
	select {
	case <-r.signalSeen:
	default:
		close(r.signalSeen)
	}
	return nil
}
func (r *fakeRemote) Shell() error { return r.Start("shell") }
func (r *fakeRemote) Start(command string) error {
	r.mu.Lock()
	r.command = command
	r.mu.Unlock()
	_, _ = io.WriteString(r.outputWriter, "instance output\n")
	_, _ = io.WriteString(r.errorWriter, "instance warning\n")
	return nil
}
func (r *fakeRemote) Wait() (int, error) {
	if r.waitGate != nil {
		<-r.waitGate
	}
	_ = r.outputWriter.Close()
	_ = r.errorWriter.Close()
	return 7, nil
}
func (r *fakeRemote) Close() error { _ = r.outputWriter.Close(); _ = r.errorWriter.Close(); return nil }

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}
func sshClientConfig(user string, signer ssh.Signer) *ssh.ClientConfig {
	return &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 2 * time.Second}
}

func startTestServer(t *testing.T, config Config, signer ssh.Signer, user string) (*ssh.Client, func()) {
	t.Helper()
	_, listener, cancel := newListeningServer(t, config)
	client, err := ssh.Dial("tcp", listener.Addr().String(), sshClientConfig(user, signer))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	return client, func() { client.Close(); cancel() }
}

func newListeningServer(t *testing.T, config Config) (*Server, net.Listener, context.CancelFunc) {
	t.Helper()
	if config.HostKeyPath == "" {
		config.HostKeyPath = filepath.Join(t.TempDir(), "host")
	}
	server, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = server.Serve(ctx, listener) }()
	return server, listener, func() { cancel(); listener.Close() }
}
