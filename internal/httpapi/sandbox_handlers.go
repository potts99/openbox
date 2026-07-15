// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/execstream"
	"github.com/openbox-dev/openbox/internal/httpapi/generated"
	"github.com/openbox-dev/openbox/internal/sandbox"
)

func (h *Handler) execInstance(response http.ResponseWriter, request *http.Request, requestID, rawID string) {
	var input generated.ExecInstanceRequest
	if err := h.decodeJSON(response, request, &input); err != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	req := sandbox.ExecRequest{Argv: append([]string(nil), input.Argv...)}
	if input.WorkingDir != nil {
		req.WorkingDir = *input.WorkingDir
	}
	if input.Env != nil {
		req.Env = *input.Env
	}
	if input.TimeoutSeconds != nil {
		req.Timeout = time.Duration(*input.TimeoutSeconds) * time.Second
	}
	if input.StdinBase64 != nil && *input.StdinBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(*input.StdinBase64)
		if err != nil {
			h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "stdin_base64")
			return
		}
		req.Stdin = bytes.NewReader(decoded)
	}

	flusher, ok := response.(http.Flusher)
	if !ok {
		h.writeError(response, requestID, http.StatusInternalServerError, "streaming_unsupported", "")
		return
	}
	response.Header().Set("Content-Type", "application/x-ndjson")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	flusher.Flush()

	sink := &ndjsonSink{response: response, flusher: flusher}
	err := h.service.Exec(request.Context(), h.requestOwner(request), domain.InstanceID(rawID), req, sink)
	if err != nil && !sink.wrote {
		_ = sink.Emit(execstream.ErrorFrame{Code: serviceErrorCode(err), Message: err.Error()})
	}
}

func (h *Handler) extendInstance(response http.ResponseWriter, request *http.Request, requestID, rawID string) {
	var input generated.ExtendInstanceRequest
	if err := h.decodeJSON(response, request, &input); err != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	if input.DurationSeconds < 1 {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "duration_seconds")
		return
	}
	instance, err := h.service.ExtendExpiry(request.Context(), h.requestOwner(request), domain.InstanceID(rawID), time.Duration(input.DurationSeconds)*time.Second)
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, mapInstance(instance, nil))
}

type ndjsonSink struct {
	response http.ResponseWriter
	flusher  http.Flusher
	wrote    bool
}

func (s *ndjsonSink) Emit(frame execstream.Frame) error {
	encoded, err := execstream.Encode(frame)
	if err != nil {
		return err
	}
	if _, err := s.response.Write(encoded); err != nil {
		return err
	}
	if _, err := s.response.Write([]byte("\n")); err != nil {
		return err
	}
	s.wrote = true
	s.flusher.Flush()
	return nil
}

func serviceErrorCode(err error) string {
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		return string(domainErr.Code)
	}
	return string(domain.CodeUnavailable)
}
