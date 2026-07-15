// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

const maxWriteFileBytes = 64 << 20

// WriteFile writes raw bytes into a managed instance via the Incus files API.
// This is the default path for guest file content (binaries, configs); do not
// route multi-MiB payloads through Exec Stdin.
func (a *Adapter) WriteFile(ctx context.Context, request runtimeapi.WriteFileRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.Ref == "" || runtimeapi.IsHostConsoleTarget(request.Ref) {
		return runtimeapi.ErrHostTarget
	}
	if request.Path == "" || request.Path[0] != '/' {
		return fmt.Errorf("write file: path must be absolute")
	}
	if request.Body == nil {
		return fmt.Errorf("write file: body is required")
	}
	mode := request.Mode
	if mode == 0 {
		mode = 0o644
	}

	ctx, cancel := context.WithTimeout(ctx, a.operationTimeout)
	defer cancel()

	query := url.Values{
		"project": {a.project},
		"path":    {request.Path},
	}
	endpoint := "http://incus/1.0/instances/" + url.PathEscape(request.Ref) + "/files?" + query.Encode()
	limited := io.LimitReader(request.Body, maxWriteFileBytes+1)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, limited)
	if err != nil {
		return fmt.Errorf("create Incus file request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Content-Type", "application/octet-stream")
	httpReq.Header.Set("X-Incus-type", "file")
	httpReq.Header.Set("X-Incus-write", "overwrite")
	httpReq.Header.Set("X-Incus-mode", formatFileMode(mode))
	httpReq.Header.Set("X-Incus-uid", strconv.Itoa(request.UID))
	httpReq.Header.Set("X-Incus-gid", strconv.Itoa(request.GID))

	response, err := a.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("push Incus file: %w", err)
	}
	defer response.Body.Close()

	var envelope apiResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("decode Incus file response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || envelope.Type == "error" {
		message := envelope.Error
		if message == "" {
			message = response.Status
		}
		statusCode := response.StatusCode
		if envelope.ErrorCode != 0 {
			statusCode = envelope.ErrorCode
		}
		httpErr := &HTTPError{StatusCode: statusCode, Message: message}
		if isNotFound(httpErr) {
			return runtimeapi.ErrNotFound
		}
		return fmt.Errorf("push Incus file: %w", httpErr)
	}
	return nil
}

func formatFileMode(mode os.FileMode) string {
	return fmt.Sprintf("%04o", mode.Perm())
}
