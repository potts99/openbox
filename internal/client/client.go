// SPDX-License-Identifier: AGPL-3.0-only

package client

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
	"path"
	"strings"
	"sync"
	"time"
)

const DefaultBaseURL = "http://127.0.0.1:8443"

type Options struct {
	BaseURL    string
	HTTPClient *http.Client
	UserAgent  string
	MaxRetries int
	RetryWait  time.Duration
}

type Client struct {
	baseURL    *url.URL
	http       *http.Client
	userAgent  string
	maxRetries int
	retryWait  time.Duration

	mu            sync.RWMutex
	serverVersion string
}

func New(options Options) (*Client, error) {
	if options.BaseURL == "" {
		options.BaseURL = DefaultBaseURL
	}
	baseURL, err := url.Parse(options.BaseURL)
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" {
		return nil, fmt.Errorf("invalid OpenBox server URL %q", options.BaseURL)
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if options.MaxRetries == 0 {
		options.MaxRetries = 2
	}
	if options.MaxRetries < 0 {
		options.MaxRetries = 0
	}
	if options.RetryWait <= 0 {
		options.RetryWait = 100 * time.Millisecond
	}
	return &Client{baseURL: baseURL, http: options.HTTPClient, userAgent: options.UserAgent, maxRetries: options.MaxRetries, retryWait: options.RetryWait}, nil
}

func (c *Client) ServerVersion() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serverVersion
}

func (c *Client) Negotiate(ctx context.Context) (Health, error) {
	var health Health
	response, err := c.do(ctx, http.MethodGet, "/v1/health", "", nil, &health)
	if err != nil {
		return Health{}, err
	}
	if err := health.validateStatus(); err != nil {
		return Health{}, err
	}
	supported := response.Header.Get(APIVersionHeader) == APIVersionV1 || health.APIVersion == APIVersionV1
	if !supported {
		return Health{}, &VersionError{Wanted: APIVersionV1, Supported: []string{health.APIVersion}}
	}
	c.mu.Lock()
	c.serverVersion = health.ServerVersion
	c.mu.Unlock()
	return health, nil
}

func (c *Client) Health(ctx context.Context) (Health, error) {
	var result Health
	_, err := c.do(ctx, http.MethodGet, "/v1/health", "", nil, &result)
	if err == nil {
		err = result.validate()
	}
	return result, err
}

func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var result Capabilities
	_, err := c.do(ctx, http.MethodGet, "/v1/capabilities", "", nil, &result)
	if err == nil {
		err = result.validate()
	}
	return result, err
}

func (c *Client) ListInstances(ctx context.Context) ([]Instance, error) {
	var envelope struct {
		Instances []Instance `json:"items"`
	}
	_, err := c.do(ctx, http.MethodGet, "/v1/instances", "", nil, &envelope)
	if err != nil {
		return nil, err
	}
	for _, instance := range envelope.Instances {
		if err := instance.validate(); err != nil {
			return nil, err
		}
	}
	return envelope.Instances, nil
}

func (c *Client) GetInstance(ctx context.Context, id string) (Instance, error) {
	var instance Instance
	_, err := c.do(ctx, http.MethodGet, resourcePath("/v1/instances", id), "", nil, &instance)
	if err == nil {
		err = instance.validate()
	}
	return instance, err
}

func (c *Client) CreateInstance(ctx context.Context, request CreateInstanceRequest, idempotencyKey string) (MutationResult, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return MutationResult{}, errors.New("idempotency key is required")
	}
	var result MutationResult
	_, err := c.do(ctx, http.MethodPost, "/v1/instances", idempotencyKey, request, &result)
	if err != nil {
		return MutationResult{}, err
	}
	return validateMutationResult(result)
}

func (c *Client) StartInstance(ctx context.Context, id, idempotencyKey string) (MutationResult, error) {
	return c.instanceAction(ctx, id, "start", idempotencyKey)
}

func (c *Client) StopInstance(ctx context.Context, id, idempotencyKey string) (MutationResult, error) {
	return c.instanceAction(ctx, id, "stop", idempotencyKey)
}

func (c *Client) RestartInstance(ctx context.Context, id, idempotencyKey string) (MutationResult, error) {
	return c.instanceAction(ctx, id, "restart", idempotencyKey)
}

func (c *Client) DeleteInstance(ctx context.Context, id, idempotencyKey string) (MutationResult, error) {
	return c.mutateOperation(ctx, http.MethodDelete, resourcePath("/v1/instances", id), idempotencyKey)
}

func (c *Client) CancelOperation(ctx context.Context, id string) (Operation, error) {
	var operation Operation
	_, err := c.do(ctx, http.MethodPost, resourcePath("/v1/operations", id, "cancel"), "", nil, &operation)
	if err == nil {
		err = operation.validate()
	}
	return operation, err
}

func (c *Client) GetOperation(ctx context.Context, id string) (Operation, error) {
	var operation Operation
	_, err := c.do(ctx, http.MethodGet, resourcePath("/v1/operations", id), "", nil, &operation)
	if err == nil {
		err = operation.validate()
	}
	return operation, err
}

func (c *Client) instanceAction(ctx context.Context, id, action, idempotencyKey string) (MutationResult, error) {
	return c.mutateOperation(ctx, http.MethodPost, resourcePath("/v1/instances", id, "actions", action), idempotencyKey)
}

func (c *Client) mutateOperation(ctx context.Context, method, requestPath, idempotencyKey string) (MutationResult, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return MutationResult{}, errors.New("idempotency key is required")
	}
	var operation Operation
	_, err := c.do(ctx, method, requestPath, idempotencyKey, nil, &operation)
	if err != nil {
		return MutationResult{}, err
	}
	return validateMutationResult(MutationResult{Operation: operation})
}

func validateMutationResult(result MutationResult) (MutationResult, error) {
	if err := result.Operation.validate(); err != nil {
		return MutationResult{}, err
	}
	if result.Instance != nil {
		if err := result.Instance.validate(); err != nil {
			return MutationResult{}, err
		}
	}
	return result, nil
}

func (c *Client) do(ctx context.Context, method, requestPath, idempotencyKey string, requestBody, responseBody any) (*http.Response, error) {
	var encoded []byte
	var err error
	if requestBody != nil {
		encoded, err = json.Marshal(requestBody)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
	}
	retryableRequest := method == http.MethodGet || method == http.MethodHead || idempotencyKey != ""
	for attempt := 0; ; attempt++ {
		request, err := c.request(ctx, method, requestPath, idempotencyKey, encoded)
		if err != nil {
			return nil, err
		}
		response, err := c.http.Do(request)
		if err != nil {
			if retryableRequest && attempt < c.maxRetries && transientNetworkError(err) {
				if err := wait(ctx, c.retryWait); err != nil {
					return nil, err
				}
				continue
			}
			return nil, fmt.Errorf("OpenBox API request: %w", err)
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			var decodeErr error
			if responseBody != nil && response.StatusCode != http.StatusNoContent {
				decodeErr = json.NewDecoder(response.Body).Decode(responseBody)
			}
			_ = response.Body.Close()
			if decodeErr != nil {
				if retryableRequest && attempt < c.maxRetries && transientResponseError(decodeErr) {
					if err := wait(ctx, c.retryWait); err != nil {
						return response, err
					}
					continue
				}
				return response, fmt.Errorf("decode OpenBox API response: %w", decodeErr)
			}
			return response, nil
		}
		apiErr := decodeAPIError(response)
		if retryableRequest && attempt < c.maxRetries && retryableStatus(response.StatusCode) {
			if err := wait(ctx, c.retryWait); err != nil {
				return response, err
			}
			continue
		}
		return response, apiErr
	}
}

func (c *Client) request(ctx context.Context, method, requestPath, idempotencyKey string, body []byte) (*http.Request, error) {
	target := *c.baseURL
	target.Path = path.Join(strings.TrimSuffix(c.baseURL.Path, "/"), requestPath)
	request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set(APIVersionHeader, APIVersionV1)
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if c.userAgent != "" {
		request.Header.Set("User-Agent", c.userAgent)
	}
	return request, nil
}

func decodeAPIError(response *http.Response) error {
	defer response.Body.Close()
	var envelope struct {
		Error APIError `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil {
		return &APIError{StatusCode: response.StatusCode, Code: "http_error", Message: response.Status}
	}
	envelope.Error.StatusCode = response.StatusCode
	if envelope.Error.Code == "" {
		envelope.Error.Code = "http_error"
	}
	return &envelope.Error
}

func resourcePath(base string, components ...string) string {
	parts := append([]string{base}, components...)
	return path.Join(parts...)
}

func transientNetworkError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}

func transientResponseError(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || transientNetworkError(err)
}

func retryableStatus(status int) bool {
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
