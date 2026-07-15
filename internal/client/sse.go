// SPDX-License-Identifier: AGPL-3.0-only

package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

type WatchOptions struct {
	AfterSequence int
	Reconnect     bool
}

func (c *Client) WatchOperation(ctx context.Context, id string, options WatchOptions) (<-chan OperationEvent, <-chan error) {
	events := make(chan OperationEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		lastID := ""
		if options.AfterSequence > 0 {
			lastID = strconv.Itoa(options.AfterSequence)
		}
		for {
			terminal, nextID, err := c.watchOnce(ctx, id, lastID, events)
			if err != nil {
				if options.Reconnect && reconnectableWatchError(err) {
					lastID = nextID
					if waitErr := wait(ctx, c.retryWait); waitErr == nil {
						continue
					}
				}
				if ctx.Err() != nil {
					errs <- ctx.Err()
				} else {
					errs <- err
				}
				return
			}
			lastID = nextID
			if terminal || !options.Reconnect {
				return
			}
			operation, err := c.GetOperation(ctx, id)
			if err != nil {
				errs <- err
				return
			}
			if operation.Terminal() {
				return
			}
			if err := wait(ctx, c.retryWait); err != nil {
				errs <- err
				return
			}
		}
	}()
	return events, errs
}

func (c *Client) watchOnce(ctx context.Context, id, lastID string, events chan<- OperationEvent) (bool, string, error) {
	request, err := c.request(ctx, http.MethodGet, resourcePath("/v1/operations", id, "events"), "", nil)
	if err != nil {
		return false, lastID, err
	}
	request.Header.Set("Accept", "text/event-stream")
	if lastID != "" {
		request.Header.Set("Last-Event-ID", lastID)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return false, lastID, fmt.Errorf("watch operation: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, lastID, decodeAPIError(response)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		response.Body.Close()
		return false, lastID, fmt.Errorf("watch operation: expected text/event-stream, got %q", response.Header.Get("Content-Type"))
	}
	defer response.Body.Close()
	return parseSSE(ctx, response.Body, lastID, events)
}

func reconnectableWatchError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable || retryableStatus(apiErr.StatusCode)
	}
	return transientNetworkError(err) || errors.Is(err, io.ErrUnexpectedEOF)
}

func parseSSE(ctx context.Context, reader io.Reader, lastID string, events chan<- OperationEvent) (bool, string, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var id, eventType string
	var data []string
	dispatch := func() (bool, error) {
		if len(data) == 0 {
			id, eventType = "", ""
			return false, nil
		}
		if eventType != "" && eventType != "operation" {
			id, eventType, data = "", "", nil
			return false, nil
		}
		var event OperationEvent
		if err := json.Unmarshal([]byte(strings.Join(data, "\n")), &event); err != nil {
			return false, fmt.Errorf("decode operation event: %w", err)
		}
		if err := event.validate(); err != nil {
			return false, err
		}
		if id != "" {
			lastID = id
		}
		select {
		case events <- event:
		case <-ctx.Done():
			return false, ctx.Err()
		}
		terminal := event.Status == OperationSucceeded || event.Status == OperationFailed
		id, eventType, data = "", "", nil
		return terminal, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			terminal, err := dispatch()
			if err != nil || terminal {
				return terminal, lastID, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			id = value
		case "event":
			eventType = value
		case "data":
			data = append(data, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return false, lastID, fmt.Errorf("read operation events: %w", err)
	}
	terminal, err := dispatch()
	return terminal, lastID, err
}
