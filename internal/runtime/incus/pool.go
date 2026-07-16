// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/cloudinit"
	"github.com/openbox-dev/openbox/internal/domain"
	sandboxpool "github.com/openbox-dev/openbox/internal/sandbox/pool"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

const poolResourceLabel = "user.openbox.resource"

// CreatePoolContainer provisions an internal warm-pool container.
func (a *Adapter) CreatePoolContainer(ctx context.Context, request sandboxpool.PoolCreateRequest) (runtimeapi.Instance, error) {
	if request.Ref == "" || request.Image == "" {
		return runtimeapi.Instance{}, errors.New("pool ref and image fingerprint are required")
	}
	config := map[string]string{
		ManagedLabel:    "true",
		poolResourceLabel: "pool",
		"security.privileged": "false",
	}
	for key, value := range request.Metadata {
		if !strings.HasPrefix(key, "user.openbox.") {
			return runtimeapi.Instance{}, fmt.Errorf("unsupported pool metadata key %q", key)
		}
		config[key] = value
	}
	if config[sandboxpool.RoleLabel] == "" {
		return runtimeapi.Instance{}, errors.New("pool role metadata is required")
	}
	if request.OwnerPublicKey != "" {
		userData, err := cloudinit.OwnerKey(request.OwnerPublicKey)
		if err != nil {
			return runtimeapi.Instance{}, fmt.Errorf("build pool cloud-init data: %w", err)
		}
		config["cloud-init.user-data"] = userData
	}
	devices := map[string]map[string]string(nil)
	if a.storagePool != "" {
		devices = map[string]map[string]string{
			"root": {"type": "disk", "path": "/", "pool": a.storagePool, "size": strconv.FormatInt(sandboxpool.DefaultDiskBytes, 10) + "B"},
		}
	}
	body := struct {
		Name     string                       `json:"name"`
		Type     string                       `json:"type"`
		Source   map[string]string            `json:"source"`
		Profiles []string                     `json:"profiles"`
		Config   map[string]string            `json:"config"`
		Devices  map[string]map[string]string `json:"devices,omitempty"`
	}{
		Name: request.Ref, Type: "container",
		Source: map[string]string{"type": "image", "fingerprint": request.Image},
		Profiles: []string{a.containerProfile}, Config: config, Devices: devices,
	}
	err := a.request(ctx, http.MethodPost, "/1.0/instances", url.Values{"project": {a.project}}, body, nil)
	if err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusConflict {
			return runtimeapi.Instance{}, runtimeapi.ErrAlreadyExists
		}
		return runtimeapi.Instance{}, fmt.Errorf("create pool container: %w", err)
	}
	return a.InspectInstance(ctx, request.Ref)
}

// UpdateInstanceConfig patches Incus instance config keys.
func (a *Adapter) UpdateInstanceConfig(ctx context.Context, ref string, values map[string]string) error {
	if ref == "" || len(values) == 0 {
		return errors.New("instance ref and config values are required")
	}
	for key := range values {
		if !strings.HasPrefix(key, "user.openbox.") && key != "cloud-init.user-data" && !strings.HasPrefix(key, "limits.") {
			return fmt.Errorf("unsupported config key %q", key)
		}
	}
	body := map[string]any{"config": values}
	if err := a.request(ctx, http.MethodPatch, "/1.0/instances/"+url.PathEscape(ref), url.Values{"project": {a.project}}, body, nil); err != nil {
		return fmt.Errorf("update instance config: %w", err)
	}
	return nil
}

// RenameInstance renames a stopped Incus instance.
func (a *Adapter) RenameInstance(ctx context.Context, ref, newRef string) error {
	if ref == "" || newRef == "" {
		return errors.New("old and new instance refs are required")
	}
	body := map[string]any{"name": newRef}
	err := a.request(ctx, http.MethodPost, "/1.0/instances/"+url.PathEscape(ref), url.Values{"project": {a.project}}, body, nil)
	if isNotFound(err) {
		return runtimeapi.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("rename Incus instance: %w", err)
	}
	return nil
}

// EnableBootstrapEgress allows outbound internet during golden-template first
// boot so cloud-init can install openssh-server before the snapshot is taken.
func (a *Adapter) EnableBootstrapEgress(ctx context.Context, ref string) error {
	return a.setInstanceNICACLs(ctx, ref, NICACLs(domain.EgressStandard))
}

// WaitSSHReady polls until TCP port 22 accepts on the managed private address.
func (a *Adapter) WaitSSHReady(ctx context.Context, ref string) error {
	if ref == "" {
		return errors.New("instance ref is required")
	}
	poll := a.readinessPoll
	if poll <= 0 {
		poll = time.Second
	}
	var lastErr error
	for {
		address, err := a.InstanceSSHAddress(ctx, ref)
		if err == nil {
			ready, probeErr := a.probeSSH(ctx, address)
			if ready {
				return nil
			}
			if probeErr != nil {
				lastErr = probeErr
			}
		} else {
			lastErr = err
		}
		if err := waitPoll(ctx, poll); err != nil {
			if lastErr != nil {
				return fmt.Errorf("SSH readiness timed out (%v): %w", lastErr, err)
			}
			return fmt.Errorf("SSH readiness timed out: %w", err)
		}
	}
}
