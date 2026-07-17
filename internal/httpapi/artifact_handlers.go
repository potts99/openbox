// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/artifacts"
	"github.com/openbox-dev/openbox/internal/domain"
)

const artifactUploadTimeout = 15 * time.Minute

type artifactResponse struct {
	ID          string    `json:"id"`
	InstanceID  string    `json:"instance_id"`
	Path        string    `json:"path"`
	SizeBytes   int64     `json:"size_bytes"`
	ContentType string    `json:"content_type"`
	SHA256      string    `json:"sha256"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (h *Handler) routeArtifacts(response http.ResponseWriter, request *http.Request, requestID, instanceID string, rest []string) bool {
	if h.artifacts == nil {
		h.writeError(response, requestID, http.StatusNotImplemented, string(domain.CodeNotImplemented), "artifacts")
		return true
	}
	if len(rest) == 0 {
		if h.requireMethod(response, request, requestID, http.MethodGet) {
			h.listArtifacts(response, request, requestID, instanceID)
		}
		return true
	}
	content := rest[len(rest)-1] == "content"
	pathParts := rest
	if content {
		pathParts = rest[:len(rest)-1]
	}
	path := strings.Join(pathParts, "/")
	if content {
		if h.requireMethod(response, request, requestID, http.MethodGet) {
			h.downloadArtifact(response, request, requestID, instanceID, path)
		}
		return true
	}
	switch request.Method {
	case http.MethodPut:
		h.putArtifact(response, request, requestID, instanceID, path)
	case http.MethodGet:
		h.inspectArtifact(response, request, requestID, instanceID, path)
	case http.MethodDelete:
		h.deleteArtifact(response, request, requestID, instanceID, path)
	default:
		h.methodNotAllowed(response, requestID, http.MethodPut, http.MethodGet, http.MethodDelete)
	}
	return true
}

func (h *Handler) listArtifacts(response http.ResponseWriter, request *http.Request, requestID, instanceID string) {
	items, err := h.artifacts.List(request.Context(), h.requestOwner(request), domain.InstanceID(instanceID), request.URL.Query().Get("prefix"))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	out := make([]artifactResponse, 0, len(items))
	for _, item := range items {
		out = append(out, mapArtifact(item))
	}
	h.writeJSON(response, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) inspectArtifact(response http.ResponseWriter, request *http.Request, requestID, instanceID, path string) {
	artifact, body, err := h.artifacts.Get(request.Context(), h.requestOwner(request), domain.InstanceID(instanceID), path)
	if body != nil {
		_ = body.Close()
	}
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, mapArtifact(artifact))
}

func (h *Handler) putArtifact(response http.ResponseWriter, request *http.Request, requestID, instanceID, path string) {
	if request.ContentLength < 0 || request.ContentLength > artifacts.MaxArtifactBytes {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "size_bytes")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), artifactUploadTimeout)
	defer cancel()
	artifact, replaced, err := h.artifacts.Put(ctx, h.requestOwner(request), domain.InstanceID(instanceID), path, request.Header.Get("Content-Type"), request.ContentLength, request.Header.Get(HeaderIdempotencyKey), request.Body)
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	status := http.StatusCreated
	if replaced {
		status = http.StatusOK
	}
	h.writeJSON(response, status, mapArtifact(artifact))
}

func (h *Handler) downloadArtifact(response http.ResponseWriter, request *http.Request, requestID, instanceID, path string) {
	artifact, body, err := h.artifacts.Get(request.Context(), h.requestOwner(request), domain.InstanceID(instanceID), path)
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	defer body.Close()
	etag := fmt.Sprintf("%q", artifact.SHA256)
	if request.Header.Get("If-None-Match") == etag {
		response.Header().Set("ETag", etag)
		response.WriteHeader(http.StatusNotModified)
		return
	}
	response.Header().Set("Content-Type", artifact.ContentType)
	response.Header().Set("Content-Length", fmt.Sprintf("%d", artifact.SizeBytes))
	response.Header().Set("ETag", etag)
	response.WriteHeader(http.StatusOK)
	_, _ = io.Copy(response, body)
}

func (h *Handler) deleteArtifact(response http.ResponseWriter, request *http.Request, requestID, instanceID, path string) {
	if err := h.artifacts.Delete(request.Context(), h.requestOwner(request), domain.InstanceID(instanceID), path); err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func mapArtifact(value domain.Artifact) artifactResponse {
	return artifactResponse{
		ID: string(value.ID), InstanceID: string(value.InstanceID), Path: value.Path, SizeBytes: value.SizeBytes,
		ContentType: value.ContentType, SHA256: value.SHA256, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}
