// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// ExportInstance writes an Incus-native instance backup stream. Incus preserves
// the instance configuration, including OpenBox ownership metadata, in this
// format.
func (a *Adapter) ExportInstance(ctx context.Context, ref string, destination io.Writer) error {
	if ref == "" || destination == nil {
		return errors.New("instance ref and backup destination are required")
	}
	response, err := a.rawRequest(ctx, http.MethodGet, "/1.0/instances/"+url.PathEscape(ref)+"/backup", url.Values{"project": {a.project}}, nil)
	if isNotFound(err) {
		return runtimeapi.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("export Incus instance: %w", err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(destination, response.Body); err != nil {
		return fmt.Errorf("write Incus instance export: %w", err)
	}
	return nil
}

// ImportInstance imports an Incus-native backup stream. Ref must be the
// instance reference recorded when the bundle was created; Incus retains that
// name from the archive and it is used to inspect the imported instance.
func (a *Adapter) ImportInstance(ctx context.Context, backup runtimeapi.InstanceBackup) (runtimeapi.Instance, error) {
	if backup.Ref == "" || backup.Body == nil {
		return runtimeapi.Instance{}, errors.New("instance backup ref and body are required")
	}
	response, err := a.rawRequest(ctx, http.MethodPost, "/1.0/instances", url.Values{"project": {a.project}}, backup.Body)
	if err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusConflict {
			return runtimeapi.Instance{}, runtimeapi.ErrAlreadyExists
		}
		return runtimeapi.Instance{}, fmt.Errorf("import Incus instance: %w", err)
	}
	defer response.Body.Close()
	var envelope apiResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&envelope); err != nil {
		return runtimeapi.Instance{}, fmt.Errorf("decode Incus import response: %w", err)
	}
	if envelope.Type == "async" && envelope.Operation != "" {
		if err := a.waitOperation(ctx, envelope.Operation); err != nil {
			return runtimeapi.Instance{}, err
		}
	}
	return a.InspectInstance(ctx, backup.Ref)
}

func (a *Adapter) rawRequest(ctx context.Context, method, path string, query url.Values, body io.Reader) (*http.Response, error) {
	endpoint := "http://incus" + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("create Incus request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/octet-stream")
	}
	response, err := a.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call Incus over %s: %w", a.socketPath, err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer response.Body.Close()
	var envelope apiResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode Incus error response: %w", err)
	}
	statusCode := response.StatusCode
	if envelope.ErrorCode != 0 {
		statusCode = envelope.ErrorCode
	}
	return nil, &HTTPError{StatusCode: statusCode, Message: envelope.Error}
}
