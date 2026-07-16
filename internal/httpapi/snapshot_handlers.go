// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"net/http"

	"github.com/openbox-dev/openbox/internal/app/clones"
	"github.com/openbox-dev/openbox/internal/app/reuse"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/httpapi/generated"
	"github.com/openbox-dev/openbox/internal/snapshots"
)

func (h *Handler) routeSnapshots(response http.ResponseWriter, request *http.Request, requestID string, rest []string) bool {
	if h.snapshots == nil {
		h.writeError(response, requestID, http.StatusNotImplemented, string(domain.CodeNotImplemented), "snapshots")
		return true
	}
	if len(rest) == 1 {
		switch request.Method {
		case http.MethodGet:
			h.getSnapshot(response, request, requestID, rest[0])
		case http.MethodDelete:
			h.deleteSnapshot(response, request, requestID, rest[0])
		default:
			h.methodNotAllowed(response, requestID, http.MethodGet, http.MethodDelete)
		}
		return true
	}
	if len(rest) == 2 && rest[1] == "restore" {
		if !h.requireMethod(response, request, requestID, http.MethodPost) {
			return true
		}
		h.restoreSnapshot(response, request, requestID, rest[0])
		return true
	}
	return false
}

func (h *Handler) listInstanceSnapshots(response http.ResponseWriter, request *http.Request, requestID, instanceID string) {
	if h.snapshots == nil {
		h.writeError(response, requestID, http.StatusNotImplemented, string(domain.CodeNotImplemented), "snapshots")
		return
	}
	items, err := h.snapshots.List(request.Context(), h.requestOwner(request), domain.InstanceID(instanceID))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	out := make([]generated.Snapshot, 0, len(items))
	for _, item := range items {
		out = append(out, mapSnapshot(item))
	}
	h.writeJSON(response, http.StatusOK, generated.ListSnapshotsResponse{Items: out})
}

func (h *Handler) createInstanceSnapshot(response http.ResponseWriter, request *http.Request, requestID, instanceID string) {
	if h.snapshots == nil {
		h.writeError(response, requestID, http.StatusNotImplemented, string(domain.CodeNotImplemented), "snapshots")
		return
	}
	key := request.Header.Get(HeaderIdempotencyKey)
	if key == "" || len(key) > 255 {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), HeaderIdempotencyKey)
		return
	}
	var input generated.CreateSnapshotRequest
	if err := h.decodeJSON(response, request, &input); err != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	snapshot, operation, err := h.snapshots.Create(request.Context(), snapshots.CreateInput{
		OwnerID: h.requestOwner(request), InstanceID: domain.InstanceID(instanceID),
		Name: input.Name, IdempotencyKey: key,
	})
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	result := generated.CreateSnapshotResult{Operation: mapOperation(operation)}
	if snapshot.ID != "" {
		mapped := mapSnapshot(snapshot)
		result.Snapshot = &mapped
	}
	h.writeJSON(response, http.StatusAccepted, result)
}

func (h *Handler) getSnapshot(response http.ResponseWriter, request *http.Request, requestID, snapshotID string) {
	snapshot, err := h.snapshots.Get(request.Context(), h.requestOwner(request), domain.SnapshotID(snapshotID))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusOK, mapSnapshot(snapshot))
}

func (h *Handler) deleteSnapshot(response http.ResponseWriter, request *http.Request, requestID, snapshotID string) {
	key := request.Header.Get(HeaderIdempotencyKey)
	if key == "" || len(key) > 255 {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), HeaderIdempotencyKey)
		return
	}
	operation, err := h.snapshots.Delete(request.Context(), h.requestOwner(request), domain.SnapshotID(snapshotID), key)
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusAccepted, mapOperation(operation))
}

func (h *Handler) restoreSnapshot(response http.ResponseWriter, request *http.Request, requestID, snapshotID string) {
	key := request.Header.Get(HeaderIdempotencyKey)
	if key == "" || len(key) > 255 {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), HeaderIdempotencyKey)
		return
	}
	var input generated.RestoreSnapshotRequest
	if err := h.decodeJSON(response, request, &input); err != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	result, err := h.snapshots.RestoreAsNew(request.Context(), snapshots.RestoreInput{
		OwnerID: h.requestOwner(request), SnapshotID: domain.SnapshotID(snapshotID),
		Name: input.Name, OwnerPublicKey: input.OwnerPublicKey, IdempotencyKey: key,
	})
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusAccepted, mapDeriveResult(result.Instance, result.Operation, result.Warnings, result.StorageEfficiency))
}

func (h *Handler) cloneInstance(response http.ResponseWriter, request *http.Request, requestID, instanceID string) {
	if h.clones == nil {
		h.writeError(response, requestID, http.StatusNotImplemented, string(domain.CodeNotImplemented), "clones")
		return
	}
	key := request.Header.Get(HeaderIdempotencyKey)
	if key == "" || len(key) > 255 {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), HeaderIdempotencyKey)
		return
	}
	var input generated.CloneInstanceRequest
	if err := h.decodeJSON(response, request, &input); err != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "body")
		return
	}
	result, err := h.clones.SubmitCopy(request.Context(), clones.CopyInput{
		OwnerID: h.requestOwner(request), Source: instanceID, Destination: input.Name,
		OwnerPublicKey: input.OwnerPublicKey, IdempotencyKey: key,
	})
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}
	h.writeJSON(response, http.StatusAccepted, mapDeriveResult(result.Instance, result.Operation, result.Warnings, result.StorageEfficiency))
}

func mapSnapshot(value domain.Snapshot) generated.Snapshot {
	return generated.Snapshot{
		Id: string(value.ID), InstanceId: string(value.InstanceID), Name: value.Name,
		Ready: value.RuntimeRef != "", CreatedAt: value.CreatedAt,
	}
}

func mapDeriveResult(instance domain.Instance, operation domain.Operation, warnings []string, efficiency reuse.StorageEfficiency) generated.DeriveInstanceResult {
	out := generated.DeriveInstanceResult{
		Operation:         mapOperation(operation),
		StorageEfficiency: generated.DeriveInstanceResultStorageEfficiency(efficiency),
	}
	if len(warnings) > 0 {
		out.Warnings = &warnings
	} else {
		empty := []string{}
		out.Warnings = &empty
	}
	if instance.ID != "" {
		mapped := mapInstance(instance, nil)
		out.Instance = &mapped
	}
	return out
}
