// SPDX-License-Identifier: AGPL-3.0-only

// Package fake provides a deterministic in-memory runtime for tests.
package fake

import (
	"context"
	"fmt"
	"sort"
	"sync"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

type Runtime struct {
	mu             sync.Mutex
	capabilities   runtimeapi.Capabilities
	images         map[string]runtimeapi.Image
	instances      map[string]runtimeapi.Instance
	execResults    map[string]runtimeapi.ExecResult
	failures       map[string][]error
	calls          []string
	createRequests []runtimeapi.CreateRequest
}

func New(capabilities runtimeapi.Capabilities) *Runtime {
	return &Runtime{
		capabilities: capabilities,
		images:       map[string]runtimeapi.Image{},
		instances:    map[string]runtimeapi.Instance{},
		execResults:  map[string]runtimeapi.ExecResult{},
		failures:     map[string][]error{},
	}
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
