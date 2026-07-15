// SPDX-License-Identifier: AGPL-3.0-only

// Package fake provides a deterministic in-memory runtime for tests.
package fake

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

type Runtime struct {
	mu                 sync.Mutex
	capabilities       runtimeapi.Capabilities
	images             map[string]runtimeapi.Image
	instances          map[string]runtimeapi.Instance
	usage              map[string]runtimeapi.UsageSnapshot
	execResults        map[string]runtimeapi.ExecResult
	failures           map[string][]error
	calls              []string
	createRequests     []runtimeapi.CreateRequest
	lastConsoleRef     string
	lastConsoleCommand []string
	consoleSizes       map[string]consoleSize
	activeConsoles     map[string]*consoleSession
	consoleExitCode    int
}

type consoleSize struct {
	cols, rows uint16
}

func New(capabilities runtimeapi.Capabilities) *Runtime {
	return &Runtime{
		capabilities:   capabilities,
		images:         map[string]runtimeapi.Image{},
		instances:      map[string]runtimeapi.Instance{},
		usage:          map[string]runtimeapi.UsageSnapshot{},
		execResults:    map[string]runtimeapi.ExecResult{},
		failures:       map[string][]error{},
		consoleSizes:   map[string]consoleSize{},
		activeConsoles: map[string]*consoleSession{},
	}
}

func (r *Runtime) SetUsage(ref string, usage runtimeapi.UsageSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage[ref] = usage
}

func (r *Runtime) InstanceUsage(ctx context.Context, ref string) (runtimeapi.UsageSnapshot, error) {
	if err := r.begin(ctx, "instance_usage"); err != nil {
		return runtimeapi.UsageSnapshot{}, err
	}
	if runtimeapi.IsHostConsoleTarget(ref) {
		return runtimeapi.UsageSnapshot{}, runtimeapi.ErrHostTarget
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	usage, ok := r.usage[ref]
	if !ok {
		instance, exists := r.instances[ref]
		if !exists {
			return runtimeapi.UsageSnapshot{}, runtimeapi.ErrNotFound
		}
		return runtimeapi.UsageSnapshot{Status: instance.State}, nil
	}
	return usage, nil
}

func (r *Runtime) AddImage(image runtimeapi.Image) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.images[image.Fingerprint] = cloneImage(image)
}

func (r *Runtime) RemoveImage(fingerprint string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.images, fingerprint)
}

func (r *Runtime) SetExecResult(ref string, result runtimeapi.ExecResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.execResults[ref] = cloneExecResult(result)
}

func (r *Runtime) FailNext(operation string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures[operation] = append(r.failures[operation], err)
}

func (r *Runtime) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *Runtime) CreateRequests() []runtimeapi.CreateRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]runtimeapi.CreateRequest, len(r.createRequests))
	for index, request := range r.createRequests {
		result[index] = cloneCreateRequest(request)
	}
	return result
}

func (r *Runtime) DiscoverCapabilities(ctx context.Context) (runtimeapi.Capabilities, error) {
	if err := r.begin(ctx, "capabilities"); err != nil {
		return runtimeapi.Capabilities{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneCapabilities(r.capabilities), nil
}

func (r *Runtime) ListImages(ctx context.Context) ([]runtimeapi.Image, error) {
	if err := r.begin(ctx, "images.list"); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0, len(r.images))
	for key := range r.images {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]runtimeapi.Image, 0, len(keys))
	for _, key := range keys {
		result = append(result, cloneImage(r.images[key]))
	}
	return result, nil
}

func (r *Runtime) InspectInstance(ctx context.Context, ref string) (runtimeapi.Instance, error) {
	if err := r.begin(ctx, "instance.inspect"); err != nil {
		return runtimeapi.Instance{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	instance, ok := r.instances[ref]
	if !ok {
		return runtimeapi.Instance{}, runtimeapi.ErrNotFound
	}
	return cloneInstance(instance), nil
}

func (r *Runtime) ListInstances(ctx context.Context) ([]runtimeapi.Instance, error) {
	if err := r.begin(ctx, "instances.list"); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	refs := make([]string, 0, len(r.instances))
	for ref := range r.instances {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	result := make([]runtimeapi.Instance, 0, len(refs))
	for _, ref := range refs {
		result = append(result, cloneInstance(r.instances[ref]))
	}
	return result, nil
}

func (r *Runtime) CreateInstance(ctx context.Context, request runtimeapi.CreateRequest) (runtimeapi.Instance, error) {
	if err := r.begin(ctx, "instance.create"); err != nil {
		return runtimeapi.Instance{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.instances[request.Ref]; exists {
		return runtimeapi.Instance{}, runtimeapi.ErrAlreadyExists
	}
	if _, exists := r.images[request.Image]; !exists {
		return runtimeapi.Instance{}, runtimeapi.ErrNotFound
	}
	r.createRequests = append(r.createRequests, cloneCreateRequest(request))
	instance := runtimeapi.Instance{
		Ref: request.Ref, Image: request.Image, State: runtimeapi.StateStopped, IsVM: request.VM,
		Metadata: cloneStringMap(request.Metadata), Resources: request.Resources, Privileged: !request.Unprivileged,
	}
	r.instances[request.Ref] = instance
	return cloneInstance(instance), nil
}

func (r *Runtime) StartInstance(ctx context.Context, ref string) error {
	return r.setState(ctx, "instance.start", ref, runtimeapi.StateRunning)
}

func (r *Runtime) WaitInstanceReady(ctx context.Context, request runtimeapi.ReadinessRequest) error {
	if err := r.begin(ctx, "instance.wait_ready"); err != nil {
		return err
	}
	if request.Stage != nil {
		if err := request.Stage("waiting_for_agent"); err != nil {
			return err
		}
		if err := request.Stage("waiting_for_ssh"); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	instance, exists := r.instances[request.Ref]
	if !exists {
		return runtimeapi.ErrNotFound
	}
	if !instance.IsVM || instance.State != runtimeapi.StateRunning {
		return runtimeapi.ErrUnsupported
	}
	return nil
}

func (r *Runtime) StopInstance(ctx context.Context, ref string) error {
	return r.setState(ctx, "instance.stop", ref, runtimeapi.StateStopped)
}

func (r *Runtime) Exec(ctx context.Context, request runtimeapi.ExecRequest) (runtimeapi.ExecResult, error) {
	if err := r.begin(ctx, "instance.exec"); err != nil {
		return runtimeapi.ExecResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	instance, exists := r.instances[request.Ref]
	if !exists {
		return runtimeapi.ExecResult{}, runtimeapi.ErrNotFound
	}
	if instance.State != runtimeapi.StateRunning {
		return runtimeapi.ExecResult{}, fmt.Errorf("exec %s: %w", request.Ref, runtimeapi.ErrUnsupported)
	}
	return cloneExecResult(r.execResults[request.Ref]), nil
}

func (r *Runtime) LastConsoleRef() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastConsoleRef
}

// LastConsoleCommand returns the Command from the most recent OpenConsole call.
func (r *Runtime) LastConsoleCommand() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastConsoleCommand == nil {
		return nil
	}
	return append([]string(nil), r.lastConsoleCommand...)
}

func (r *Runtime) LastConsoleSize(ref string) (cols, rows uint16) {
	r.mu.Lock()
	defer r.mu.Unlock()
	size := r.consoleSizes[ref]
	return size.cols, size.rows
}

// SetConsoleExitCode sets the exit code returned by Wait after Close for the next
// (and subsequent) console sessions. Zero is the default.
func (r *Runtime) SetConsoleExitCode(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.consoleExitCode = code
}

// ActiveConsole returns the most recent console session for ref, if any.
func (r *Runtime) ActiveConsole(ref string) runtimeapi.ConsoleSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.activeConsoles[ref]; s != nil {
		return s
	}
	return nil
}

// ConsoleClosed reports whether the active console for ref has been Close()'d.
func (r *Runtime) ConsoleClosed(ref string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.activeConsoles[ref]
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (r *Runtime) OpenConsole(ctx context.Context, request runtimeapi.ConsoleRequest) (runtimeapi.ConsoleSession, error) {
	if runtimeapi.IsHostConsoleTarget(request.Ref) {
		return nil, runtimeapi.ErrHostTarget
	}
	if err := r.begin(ctx, "console.open"); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	instance, exists := r.instances[request.Ref]
	if !exists {
		return nil, runtimeapi.ErrNotFound
	}
	if instance.State != runtimeapi.StateRunning {
		return nil, fmt.Errorf("console %s: %w", request.Ref, runtimeapi.ErrUnsupported)
	}
	command := append([]string(nil), request.Command...)
	if len(command) == 0 {
		command = []string{"/bin/bash"}
	}
	r.lastConsoleRef = request.Ref
	r.lastConsoleCommand = append([]string(nil), command...)
	r.consoleSizes[request.Ref] = consoleSize{cols: request.Cols, rows: request.Rows}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	session := &consoleSession{
		runtime: r,
		ref:     request.Ref,
		stdin:   stdinW,
		stdout:  stdoutR,
		done:    make(chan struct{}),
	}
	r.activeConsoles[request.Ref] = session
	go func() {
		defer stdoutW.Close()
		defer stdinR.Close()
		_, _ = io.Copy(stdoutW, stdinR)
		r.mu.Lock()
		code := r.consoleExitCode
		r.mu.Unlock()
		session.finish(code, nil)
	}()
	return session, nil
}

type consoleSession struct {
	runtime *Runtime
	ref     string
	stdin   *io.PipeWriter
	stdout  *io.PipeReader

	mu       sync.Mutex
	closed   bool
	exitCode int
	waitErr  error
	done     chan struct{}
}

func (s *consoleSession) Stdin() io.WriteCloser { return s.stdin }
func (s *consoleSession) Stdout() io.Reader     { return s.stdout }

func (s *consoleSession) Resize(cols, rows uint16) error {
	s.runtime.mu.Lock()
	defer s.runtime.mu.Unlock()
	s.runtime.consoleSizes[s.ref] = consoleSize{cols: cols, rows: rows}
	return nil
}

func (s *consoleSession) Wait() (int, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCode, s.waitErr
}

func (s *consoleSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	_ = s.stdin.Close()
	s.runtime.mu.Lock()
	code := s.runtime.consoleExitCode
	s.runtime.mu.Unlock()
	s.finish(code, nil)
	return nil
}

func (s *consoleSession) finish(exitCode int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		return
	default:
		s.exitCode = exitCode
		s.waitErr = err
		close(s.done)
	}
}

func (r *Runtime) CreateSnapshot(ctx context.Context, ref, name string) error {
	if err := r.begin(ctx, "snapshot.create"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	instance, exists := r.instances[ref]
	if !exists {
		return runtimeapi.ErrNotFound
	}
	for _, snapshot := range instance.Snapshots {
		if snapshot == name {
			return runtimeapi.ErrAlreadyExists
		}
	}
	instance.Snapshots = append(instance.Snapshots, name)
	sort.Strings(instance.Snapshots)
	r.instances[ref] = instance
	return nil
}

func (r *Runtime) DeleteSnapshot(ctx context.Context, ref, name string) error {
	if err := r.begin(ctx, "snapshot.delete"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	instance, exists := r.instances[ref]
	if !exists {
		return runtimeapi.ErrNotFound
	}
	kept := make([]string, 0, len(instance.Snapshots))
	found := false
	for _, snapshot := range instance.Snapshots {
		if snapshot == name {
			found = true
			continue
		}
		kept = append(kept, snapshot)
	}
	if !found {
		return runtimeapi.ErrNotFound
	}
	instance.Snapshots = kept
	r.instances[ref] = instance
	return nil
}

func (r *Runtime) CopyInstance(ctx context.Context, request runtimeapi.CopyRequest) (runtimeapi.Instance, error) {
	if err := r.begin(ctx, "instance.copy"); err != nil {
		return runtimeapi.Instance{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.instances[request.TargetRef]; exists {
		return runtimeapi.Instance{}, runtimeapi.ErrAlreadyExists
	}
	source, exists := r.instances[request.SourceRef]
	if !exists {
		return runtimeapi.Instance{}, runtimeapi.ErrNotFound
	}
	if request.Snapshot != "" && !contains(source.Snapshots, request.Snapshot) {
		return runtimeapi.Instance{}, runtimeapi.ErrNotFound
	}
	target := source
	target.Ref = request.TargetRef
	target.Snapshots = nil
	if request.Metadata != nil {
		target.Metadata = cloneStringMap(request.Metadata)
	}
	r.instances[target.Ref] = target
	return cloneInstance(target), nil
}

func (r *Runtime) DeleteInstance(ctx context.Context, ref string) error {
	if err := r.begin(ctx, "instance.delete"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.instances[ref]; !exists {
		return runtimeapi.ErrNotFound
	}
	delete(r.instances, ref)
	return nil
}

func (r *Runtime) setState(ctx context.Context, operation, ref string, state runtimeapi.InstanceState) error {
	if err := r.begin(ctx, operation); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	instance, exists := r.instances[ref]
	if !exists {
		return runtimeapi.ErrNotFound
	}
	instance.State = state
	r.instances[ref] = instance
	return nil
}

func (r *Runtime) begin(ctx context.Context, operation string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, operation)
	if failures := r.failures[operation]; len(failures) > 0 {
		err := failures[0]
		r.failures[operation] = failures[1:]
		return err
	}
	return nil
}

func cloneCapabilities(value runtimeapi.Capabilities) runtimeapi.Capabilities {
	value.Namespaces = cloneMap(value.Namespaces)
	value.NetworkTools = cloneMap(value.NetworkTools)
	value.StorageDrivers = append([]string(nil), value.StorageDrivers...)
	return value
}

func cloneMap(value map[string]bool) map[string]bool {
	if value == nil {
		return nil
	}
	result := make(map[string]bool, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func cloneImage(value runtimeapi.Image) runtimeapi.Image {
	value.Aliases = append([]string(nil), value.Aliases...)
	return value
}

func cloneInstance(value runtimeapi.Instance) runtimeapi.Instance {
	value.Metadata = cloneStringMap(value.Metadata)
	value.Snapshots = append([]string(nil), value.Snapshots...)
	return value
}

func cloneExecResult(value runtimeapi.ExecResult) runtimeapi.ExecResult {
	value.Stdout = append([]byte(nil), value.Stdout...)
	value.Stderr = append([]byte(nil), value.Stderr...)
	return value
}

func cloneCreateRequest(value runtimeapi.CreateRequest) runtimeapi.CreateRequest {
	value.Metadata = cloneStringMap(value.Metadata)
	return value
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

var _ runtimeapi.Runtime = (*Runtime)(nil)
