// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// CreateImageBuilder creates an ephemeral, standard-egress container from the
// checked-in base alias. It intentionally does not use the sandbox profile:
// package installation must be able to reach its declared repositories.
func (a *Adapter) CreateImageBuilder(ctx context.Context, ref, base, architecture, runtime string) error {
	if ref == "" || base == "" {
		return errors.New("builder ref and base image are required")
	}
	if runtime != "container" && runtime != "virtual-machine" {
		return fmt.Errorf("unsupported image build runtime %q", runtime)
	}
	if a.storagePool == "" {
		return errors.New("image builds require a configured Incus storage pool")
	}
	body := struct {
		Name    string                       `json:"name"`
		Type    string                       `json:"type"`
		Source  map[string]string            `json:"source"`
		Config  map[string]string            `json:"config"`
		Devices map[string]map[string]string `json:"devices"`
	}{
		Name: ref, Type: runtime,
		Source: map[string]string{"type": "image", "alias": base, "protocol": "simplestreams", "server": "https://images.linuxcontainers.org"},
		Config: map[string]string{
			"security.privileged": "false",
			ManagedLabel:          "true",
			ResourceLabel:         "image-builder",
		},
		Devices: map[string]map[string]string{
			"root": {"type": "disk", "path": "/", "pool": a.storagePool},
			"eth0": {"type": "nic", "network": a.network, "name": "eth0", "security.acls": StandardEgressACLName},
		},
	}
	if architecture != "" {
		body.Source["architecture"] = architecture
	}
	err := a.request(ctx, http.MethodPost, "/1.0/instances", url.Values{"project": {a.project}}, body, nil)
	if err == nil {
		return nil
	}
	if isNotFound(err) {
		return fmt.Errorf("resolve builder base image %q: %w", base, err)
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusConflict {
		return runtimeapi.ErrAlreadyExists
	}
	return fmt.Errorf("create image builder: %w", err)
}

// PublishImageAlias snapshots the stopped builder and atomically repoints the
// curated alias only after Incus returned an immutable fingerprint.
func (a *Adapter) PublishImageAlias(ctx context.Context, builderRef, alias string) (string, error) {
	envelope, err := a.call(ctx, a.timeout, http.MethodPost, "/1.0/images", url.Values{"project": {a.project}}, map[string]any{
		"public": false,
		"source": map[string]string{"type": "instance", "name": builderRef},
	}, nil)
	if err != nil {
		return "", fmt.Errorf("publish builder image: %w", err)
	}
	if envelope.Type != "async" || envelope.Operation == "" {
		return "", errors.New("Incus image publish did not return an async operation")
	}
	var result struct {
		Metadata struct {
			Fingerprint string `json:"fingerprint"`
		} `json:"metadata"`
	}
	if err := a.waitOperationResult(ctx, envelope.Operation, &result); err != nil {
		return "", err
	}
	if result.Metadata.Fingerprint == "" {
		return "", errors.New("Incus image publish returned no fingerprint")
	}
	target := "/1.0/images/aliases/" + url.PathEscape(alias)
	body := map[string]string{"target": result.Metadata.Fingerprint, "description": "OpenBox devbox image"}
	if err := a.request(ctx, http.MethodPut, target, url.Values{"project": {a.project}}, body, nil); err != nil {
		if !isNotFound(err) {
			return "", fmt.Errorf("repoint image alias: %w", err)
		}
		if err := a.request(ctx, http.MethodPost, "/1.0/images/aliases", url.Values{"project": {a.project}}, map[string]string{
			"name": alias, "target": result.Metadata.Fingerprint, "description": "OpenBox devbox image",
		}, nil); err != nil {
			return "", fmt.Errorf("create image alias: %w", err)
		}
	}
	return result.Metadata.Fingerprint, nil
}
