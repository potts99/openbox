// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// CreateSnapshot creates a named Incus instance snapshot.
func (a *Adapter) CreateSnapshot(ctx context.Context, ref, name string) error {
	if ref == "" || name == "" {
		return errors.New("instance ref and snapshot name are required")
	}
	body := map[string]any{"name": name, "stateful": false}
	err := a.request(ctx, http.MethodPost, "/1.0/instances/"+url.PathEscape(ref)+"/snapshots", url.Values{"project": {a.project}}, body, nil)
	if isNotFound(err) {
		return runtimeapi.ErrNotFound
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusConflict {
		return runtimeapi.ErrAlreadyExists
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return runtimeapi.ErrAlreadyExists
		}
		return fmt.Errorf("create Incus snapshot: %w", err)
	}
	return nil
}

// DeleteSnapshot removes a named Incus instance snapshot.
func (a *Adapter) DeleteSnapshot(ctx context.Context, ref, name string) error {
	if ref == "" || name == "" {
		return errors.New("instance ref and snapshot name are required")
	}
	path := "/1.0/instances/" + url.PathEscape(ref) + "/snapshots/" + url.PathEscape(name)
	err := a.request(ctx, http.MethodDelete, path, url.Values{"project": {a.project}}, nil, nil)
	if isNotFound(err) {
		return runtimeapi.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete Incus snapshot: %w", err)
	}
	return nil
}

// CopyInstance copies an instance, optionally from a snapshot, into a new ref.
// When Metadata is set, OpenBox ownership keys are rewritten on the copy before return.
func (a *Adapter) CopyInstance(ctx context.Context, request runtimeapi.CopyRequest) (runtimeapi.Instance, error) {
	if request.SourceRef == "" || request.TargetRef == "" {
		return runtimeapi.Instance{}, errors.New("source and target refs are required")
	}
	source := map[string]string{"type": "copy", "source": request.SourceRef, "project": a.project}
	if request.Snapshot != "" {
		source["snapshot"] = request.Snapshot
	}
	body := map[string]any{"name": request.TargetRef, "source": source}
	err := a.request(ctx, http.MethodPost, "/1.0/instances", url.Values{"project": {a.project}}, body, nil)
	if isNotFound(err) {
		return runtimeapi.Instance{}, runtimeapi.ErrNotFound
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusConflict {
		return runtimeapi.Instance{}, runtimeapi.ErrAlreadyExists
	}
	if err != nil {
		return runtimeapi.Instance{}, fmt.Errorf("copy Incus instance: %w", err)
	}
	if len(request.Metadata) > 0 {
		if err := a.applyOwnershipMetadata(ctx, request.TargetRef, request.Metadata); err != nil {
			_ = a.DeleteInstance(ctx, request.TargetRef)
			return runtimeapi.Instance{}, err
		}
	}
	return a.InspectInstance(ctx, request.TargetRef)
}

func (a *Adapter) applyOwnershipMetadata(ctx context.Context, ref string, metadata map[string]string) error {
	var record instanceRecord
	if err := a.request(ctx, http.MethodGet, "/1.0/instances/"+url.PathEscape(ref), url.Values{"project": {a.project}}, nil, &record); err != nil {
		return fmt.Errorf("load copied instance for ownership rewrite: %w", err)
	}
	config := map[string]string{}
	for key, value := range record.Config {
		config[key] = value
	}
	for key, value := range metadata {
		if !strings.HasPrefix(key, "user.openbox.") {
			return fmt.Errorf("unsupported instance metadata key %q", key)
		}
		config[key] = value
	}
	body := map[string]any{"config": config}
	if err := a.request(ctx, http.MethodPatch, "/1.0/instances/"+url.PathEscape(ref), url.Values{"project": {a.project}}, body, nil); err != nil {
		return fmt.Errorf("rewrite copied instance ownership: %w", err)
	}
	return nil
}
