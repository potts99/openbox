// SPDX-License-Identifier: AGPL-3.0-only

// Package incus implements safe local-daemon discovery and bootstrap.
package incus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

const DefaultSocket = "/var/lib/incus/unix.socket"

type Options struct {
	SocketPath string
	Timeout    time.Duration
	HostProbe  HostProbe
}

type Adapter struct {
	socketPath string
	timeout    time.Duration
	client     *http.Client
	hostProbe  HostProbe
}

func New(options Options) (*Adapter, error) {
	socketPath := options.SocketPath
	if socketPath == "" {
		socketPath = DefaultSocket
	}
	if !filepath.IsAbs(socketPath) || strings.Contains(socketPath, "://") {
		return nil, fmt.Errorf("Incus socket must be an absolute local path")
	}
	timeout := options.Timeout
	if timeout < 0 {
		return nil, fmt.Errorf("Incus timeout must not be negative")
	}
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
		DisableCompression: true,
	}
	probe := options.HostProbe
	if probe == nil {
		probe = OSHostProbe{}
	}
	return &Adapter{
		socketPath: socketPath,
		timeout:    timeout,
		client:     &http.Client{Transport: transport},
		hostProbe:  probe,
	}, nil
}

type apiResponse struct {
	Type       string          `json:"type"`
	Status     string          `json:"status"`
	StatusCode int             `json:"status_code"`
	Error      string          `json:"error"`
	ErrorCode  int             `json:"error_code"`
	Operation  string          `json:"operation"`
	Metadata   json.RawMessage `json:"metadata"`
}

type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("Incus API returned %d: %s", e.StatusCode, e.Message)
}

func isNotFound(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

func (a *Adapter) request(ctx context.Context, method, path string, query url.Values, body any, output any) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode Incus request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	endpoint := "http://incus" + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return fmt.Errorf("create Incus request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("call Incus over %s: %w", a.socketPath, err)
	}
	defer response.Body.Close()

	limited := io.LimitReader(response.Body, 8<<20)
	var envelope apiResponse
	if err := json.NewDecoder(limited).Decode(&envelope); err != nil {
		return fmt.Errorf("decode Incus response: %w", err)
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
		return &HTTPError{StatusCode: statusCode, Message: message}
	}
	if envelope.Type == "async" && envelope.Operation != "" {
		return a.waitOperation(ctx, envelope.Operation)
	}
	if output != nil && len(envelope.Metadata) > 0 && string(envelope.Metadata) != "null" {
		if err := json.Unmarshal(envelope.Metadata, output); err != nil {
			return fmt.Errorf("decode Incus metadata: %w", err)
		}
	}
	return nil
}

func (a *Adapter) waitOperation(ctx context.Context, operation string) error {
	var result struct {
		StatusCode int    `json:"status_code"`
		Err        string `json:"err"`
	}
	if err := a.request(ctx, http.MethodGet, operation+"/wait", nil, nil, &result); err != nil {
		return fmt.Errorf("wait for Incus operation: %w", err)
	}
	if result.StatusCode >= 400 || result.Err != "" {
		return fmt.Errorf("Incus operation failed: %s", result.Err)
	}
	return nil
}
