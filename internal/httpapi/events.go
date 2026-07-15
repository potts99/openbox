// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/httpapi/generated"
)

func (h *Handler) streamOperationEvents(response http.ResponseWriter, request *http.Request, requestID, rawID string) {
	after, err := eventCursor(request.Header.Get("Last-Event-ID"))
	if err != nil {
		h.writeError(response, requestID, http.StatusBadRequest, string(domain.CodeInvalidArgument), "Last-Event-ID")
		return
	}
	operationID := domain.OperationID(rawID)
	operation, err := h.service.GetOperation(request.Context(), h.requestOwner(request), operationID)
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}

	flusher, ok := response.(http.Flusher)
	if !ok {
		h.writeError(response, requestID, http.StatusInternalServerError, "streaming_unsupported", "")
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache, no-store")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)

	poll := time.NewTicker(h.pollInterval)
	heartbeat := time.NewTicker(h.heartbeatInterval)
	defer poll.Stop()
	defer heartbeat.Stop()

	for {
		events, listErr := h.service.ListOperationEventsAfter(request.Context(), h.requestOwner(request), operationID, after, h.eventBatchSize)
		if listErr != nil {
			writeSSEError(response, requestID)
			flusher.Flush()
			return
		}
		for _, event := range events {
			if event.Sequence <= after {
				continue
			}
			if err := writeSSEEvent(response, event); err != nil {
				writeSSEError(response, requestID)
				flusher.Flush()
				return
			}
			after = event.Sequence
			flusher.Flush()
			if terminalStatus(event.Status) {
				return
			}
		}
		if len(events) == h.eventBatchSize {
			continue
		}
		if terminalStatus(operation.Status) {
			return
		}

		select {
		case <-request.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(response, ": heartbeat %d\n\n", time.Now().UTC().Unix()); err != nil {
				return
			}
			flusher.Flush()
		case <-poll.C:
			operation, err = h.service.GetOperation(request.Context(), h.requestOwner(request), operationID)
			if err != nil {
				writeSSEError(response, requestID)
				flusher.Flush()
				return
			}
		}
	}
}

func eventCursor(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid event cursor")
	}
	return value, nil
}

func writeSSEEvent(writer http.ResponseWriter, event domain.OperationEvent) error {
	metadata := map[string]any(nil)
	if len(event.MetadataJSON) > 0 {
		if err := json.Unmarshal(event.MetadataJSON, &metadata); err != nil {
			return fmt.Errorf("decode operation event metadata: %w", err)
		}
	}
	payload, err := json.Marshal(generated.OperationEvent{
		Sequence: event.Sequence, OperationId: string(event.OperationID), Stage: event.Stage,
		Status: generated.OperationEventStatus(event.Status), Progress: event.Progress, ErrorClass: optionalString(event.ErrorClass), ErrorCode: optionalString(string(event.ErrorCode)),
		Message: optionalString(event.Message), Metadata: optionalMap(metadata), CreatedAt: event.CreatedAt,
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "id: %d\nevent: operation\ndata: %s\n\n", event.Sequence, payload)
	return err
}

func writeSSEError(writer http.ResponseWriter, requestID string) {
	payload, _ := json.Marshal(errorEnvelope(requestID, "stream_error", "The event stream could not be continued.", ""))
	_, _ = fmt.Fprintf(writer, "event: error\ndata: %s\n\n", payload)
}

func optionalMap(value map[string]any) *map[string]any {
	if value == nil {
		return nil
	}
	return &value
}

func terminalStatus(status domain.OperationStatus) bool {
	return status == domain.OperationSucceeded || status == domain.OperationFailed
}
