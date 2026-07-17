// SPDX-License-Identifier: AGPL-3.0-only

package openbox

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
	Token      string
	MaxRetries int
	RetryWait  time.Duration
}

type Client struct {
	baseURL    *url.URL
	http       *http.Client
	streamHTTP *http.Client
	userAgent  string
	token      string
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
	streamHTTP := *options.HTTPClient
	streamHTTP.Timeout = 0
	return &Client{baseURL: baseURL, http: options.HTTPClient, streamHTTP: &streamHTTP, userAgent: options.UserAgent, token: strings.TrimSpace(options.Token), maxRetries: options.MaxRetries, retryWait: options.RetryWait}, nil
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

func (c *Client) ListImages(ctx context.Context) ([]Image, error) {
	var envelope struct {
		Items []Image `json:"items"`
	}
	_, err := c.do(ctx, http.MethodGet, "/v1/images", "", nil, &envelope)
	if err != nil {
		return nil, err
	}
	return envelope.Items, nil
}

// BuildImage submits the checked-in Devbox recipe as a durable operation.
func (c *Client) BuildImage(ctx context.Context, request BuildImageRequest, idempotencyKey string) (Operation, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return Operation{}, errors.New("idempotency key is required")
	}
	var operation Operation
	_, err := c.do(ctx, http.MethodPost, "/v1/images/build", idempotencyKey, request, &operation)
	if err == nil {
		err = operation.validate()
	}
	return operation, err
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

func (c *Client) ExtendInstance(ctx context.Context, id string, durationSeconds int) (Instance, error) {
	var instance Instance
	_, err := c.do(ctx, http.MethodPost, resourcePath("/v1/instances", id, "extend"), "", ExtendInstanceRequest{DurationSeconds: durationSeconds}, &instance)
	if err != nil {
		return Instance{}, err
	}
	return instance, instance.validate()
}

func (c *Client) ListSnapshots(ctx context.Context, instanceID string) ([]Snapshot, error) {
	var response struct {
		Items []Snapshot `json:"items"`
	}
	_, err := c.do(ctx, http.MethodGet, resourcePath("/v1/instances", instanceID, "snapshots"), "", nil, &response)
	if err != nil {
		return nil, err
	}
	if response.Items == nil {
		return []Snapshot{}, nil
	}
	return response.Items, nil
}

func (c *Client) PutArtifact(ctx context.Context, instanceID, artifactPath string, body io.Reader, sizeBytes int64, contentType, idempotencyKey string) (Artifact, error) {
	if sizeBytes < 0 {
		return Artifact{}, errors.New("artifact size must be non-negative")
	}
	request, err := c.artifactRequest(ctx, http.MethodPut, instanceID, artifactPath, "", body)
	if err != nil {
		return Artifact{}, err
	}
	request.ContentLength = sizeBytes
	request.Header.Set("Content-Length", fmt.Sprintf("%d", sizeBytes))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	} else {
		request.Header.Set("Content-Type", "application/octet-stream")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return Artifact{}, fmt.Errorf("OpenBox artifact upload: %w", err)
	}
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return Artifact{}, decodeAPIError(response)
	}
	defer response.Body.Close()
	var artifact Artifact
	if err := json.NewDecoder(response.Body).Decode(&artifact); err != nil {
		return Artifact{}, fmt.Errorf("decode OpenBox artifact response: %w", err)
	}
	return artifact, nil
}

func (c *Client) ListArtifacts(ctx context.Context, instanceID, prefix string) ([]Artifact, error) {
	request, err := c.artifactRequest(ctx, http.MethodGet, instanceID, "", "", nil)
	if err != nil {
		return nil, err
	}
	if prefix != "" {
		query := request.URL.Query()
		query.Set("prefix", prefix)
		request.URL.RawQuery = query.Encode()
	}
	var envelope struct {
		Items []Artifact `json:"items"`
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, decodeAPIError(response)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode OpenBox artifact response: %w", err)
	}
	if envelope.Items == nil {
		return []Artifact{}, nil
	}
	return envelope.Items, nil
}

func (c *Client) GetArtifact(ctx context.Context, instanceID, artifactPath string) (ArtifactDownload, error) {
	request, err := c.artifactRequest(ctx, http.MethodGet, instanceID, artifactPath, "content", nil)
	if err != nil {
		return ArtifactDownload{}, err
	}
	response, err := c.streamHTTP.Do(request)
	if err != nil {
		return ArtifactDownload{}, fmt.Errorf("OpenBox artifact download: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ArtifactDownload{}, decodeAPIError(response)
	}
	return ArtifactDownload{
		ContentType: response.Header.Get("Content-Type"),
		SizeBytes:   response.ContentLength,
		SHA256:      strings.Trim(response.Header.Get("ETag"), `"`),
		Body:        response.Body,
	}, nil
}

func (c *Client) DeleteArtifact(ctx context.Context, instanceID, artifactPath string) error {
	request, err := c.artifactRequest(ctx, http.MethodDelete, instanceID, artifactPath, "", nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("OpenBox artifact delete: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeAPIError(response)
	}
	return response.Body.Close()
}

func (c *Client) CreateSnapshot(ctx context.Context, instanceID, name, idempotencyKey string) (CreateSnapshotResult, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return CreateSnapshotResult{}, errors.New("idempotency key is required")
	}
	var result CreateSnapshotResult
	_, err := c.do(ctx, http.MethodPost, resourcePath("/v1/instances", instanceID, "snapshots"), idempotencyKey, CreateSnapshotRequest{Name: name}, &result)
	if err != nil {
		return CreateSnapshotResult{}, err
	}
	return result, result.Operation.validate()
}

func (c *Client) GetSnapshot(ctx context.Context, snapshotID string) (Snapshot, error) {
	var snapshot Snapshot
	_, err := c.do(ctx, http.MethodGet, resourcePath("/v1/snapshots", snapshotID), "", nil, &snapshot)
	return snapshot, err
}

func (c *Client) DeleteSnapshot(ctx context.Context, snapshotID, idempotencyKey string) (Operation, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return Operation{}, errors.New("idempotency key is required")
	}
	var operation Operation
	_, err := c.do(ctx, http.MethodDelete, resourcePath("/v1/snapshots", snapshotID), idempotencyKey, nil, &operation)
	if err != nil {
		return Operation{}, err
	}
	return operation, operation.validate()
}

func (c *Client) RestoreSnapshot(ctx context.Context, snapshotID string, request RestoreSnapshotRequest, idempotencyKey string) (DeriveInstanceResult, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return DeriveInstanceResult{}, errors.New("idempotency key is required")
	}
	var result DeriveInstanceResult
	_, err := c.do(ctx, http.MethodPost, resourcePath("/v1/snapshots", snapshotID, "restore"), idempotencyKey, request, &result)
	if err != nil {
		return DeriveInstanceResult{}, err
	}
	return result, result.Operation.validate()
}

func (c *Client) CloneInstance(ctx context.Context, instanceID string, request CloneInstanceRequest, idempotencyKey string) (DeriveInstanceResult, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return DeriveInstanceResult{}, errors.New("idempotency key is required")
	}
	var result DeriveInstanceResult
	_, err := c.do(ctx, http.MethodPost, resourcePath("/v1/instances", instanceID, "clone"), idempotencyKey, request, &result)
	if err != nil {
		return DeriveInstanceResult{}, err
	}
	return result, result.Operation.validate()
}

// ExecInstance streams NDJSON exec frames. The returned ReadCloser must be
// closed by the caller. Frames are never buffered server-side into SQLite.
func (c *Client) ExecInstance(ctx context.Context, id string, request ExecInstanceRequest) (io.ReadCloser, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode exec request: %w", err)
	}
	httpRequest, err := c.request(ctx, http.MethodPost, resourcePath("/v1/instances", id, "exec"), "", encoded)
	if err != nil {
		return nil, err
	}
	response, err := c.streamHTTP.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("OpenBox exec request: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, decodeAPIError(response)
	}
	return response.Body, nil
}

func (c *Client) ListOperations(ctx context.Context) ([]Operation, error) {
	var envelope struct {
		Items []Operation `json:"items"`
	}
	_, err := c.do(ctx, http.MethodGet, "/v1/operations", "", nil, &envelope)
	if err != nil {
		return nil, err
	}
	for _, operation := range envelope.Items {
		if err := operation.validate(); err != nil {
			return nil, err
		}
	}
	return envelope.Items, nil
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

func (c *Client) ListRoutes(ctx context.Context) ([]Route, error) {
	var envelope struct {
		Items []Route `json:"items"`
	}
	_, err := c.do(ctx, http.MethodGet, "/v1/routes", "", nil, &envelope)
	if err != nil {
		return nil, err
	}
	for _, route := range envelope.Items {
		if err := route.validate(); err != nil {
			return nil, err
		}
	}
	return envelope.Items, nil
}

func (c *Client) CreateRoute(ctx context.Context, request CreateRouteRequest) (Route, error) {
	var route Route
	_, err := c.do(ctx, http.MethodPost, "/v1/routes", "", request, &route)
	if err == nil {
		err = route.validate()
	}
	return route, err
}

func (c *Client) DeleteRoute(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodDelete, resourcePath("/v1/routes", id), "", nil, nil)
	return err
}

func (c *Client) PublishRoute(ctx context.Context, id string) (Route, error) {
	var route Route
	_, err := c.do(ctx, http.MethodPost, resourcePath("/v1/routes", id, "publish"), "", nil, &route)
	if err == nil {
		err = route.validate()
	}
	return route, err
}

func (c *Client) ListEgressProfiles(ctx context.Context) ([]EgressProfile, error) {
	var envelope struct {
		Items []EgressProfile `json:"items"`
	}
	_, err := c.do(ctx, http.MethodGet, "/v1/network/egress-profiles", "", nil, &envelope)
	if err != nil {
		return nil, err
	}
	if envelope.Items == nil {
		return []EgressProfile{}, nil
	}
	return envelope.Items, nil
}

func (c *Client) GetEgressProfile(ctx context.Context, id string) (EgressProfile, error) {
	var profile EgressProfile
	_, err := c.do(ctx, http.MethodGet, resourcePath("/v1/network/egress-profiles", id), "", nil, &profile)
	return profile, err
}

func (c *Client) CreateEgressProfile(ctx context.Context, name, mode string, destinations []string) (EgressProfile, error) {
	var profile EgressProfile
	_, err := c.do(ctx, http.MethodPost, "/v1/network/egress-profiles", "", map[string]any{
		"name": name, "mode": mode, "allowed_destinations": destinations,
	}, &profile)
	return profile, err
}

func (c *Client) UpdateEgressProfile(ctx context.Context, id string, patch map[string]any) (EgressProfile, []map[string]string, error) {
	var envelope struct {
		Profile     EgressProfile       `json:"profile"`
		ApplyErrors []map[string]string `json:"apply_errors"`
	}
	_, err := c.do(ctx, http.MethodPatch, resourcePath("/v1/network/egress-profiles", id), "", patch, &envelope)
	return envelope.Profile, envelope.ApplyErrors, err
}

func (c *Client) DeleteEgressProfile(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodDelete, resourcePath("/v1/network/egress-profiles", id), "", nil, nil)
	return err
}

func (c *Client) ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	path := "/v1/audit-events"
	if limit > 0 {
		path = fmt.Sprintf("/v1/audit-events?limit=%d", limit)
	}
	var response struct {
		Items []AuditEvent `json:"items"`
	}
	if _, err := c.do(ctx, http.MethodGet, path, "", nil, &response); err != nil {
		return nil, err
	}
	if response.Items == nil {
		return []AuditEvent{}, nil
	}
	return response.Items, nil
}

func (c *Client) ListWebhookSubscriptions(ctx context.Context) ([]WebhookSubscription, error) {
	var response struct {
		Items []WebhookSubscription `json:"items"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/v1/webhook-subscriptions", "", nil, &response); err != nil {
		return nil, err
	}
	if response.Items == nil {
		return []WebhookSubscription{}, nil
	}
	return response.Items, nil
}

func (c *Client) GetWebhookSubscription(ctx context.Context, id string) (WebhookSubscription, error) {
	var subscription WebhookSubscription
	_, err := c.do(ctx, http.MethodGet, resourcePath("/v1/webhook-subscriptions", id), "", nil, &subscription)
	return subscription, err
}

// CreateWebhookSubscription returns the signing secret exactly once.
func (c *Client) CreateWebhookSubscription(ctx context.Context, request CreateWebhookSubscriptionRequest) (CreatedWebhookSubscription, error) {
	var subscription CreatedWebhookSubscription
	_, err := c.do(ctx, http.MethodPost, "/v1/webhook-subscriptions", "", request, &subscription)
	return subscription, err
}

// UpdateWebhookSubscription returns a secret only when RotateSecret is true.
func (c *Client) UpdateWebhookSubscription(ctx context.Context, id string, request UpdateWebhookSubscriptionRequest) (CreatedWebhookSubscription, error) {
	var subscription CreatedWebhookSubscription
	_, err := c.do(ctx, http.MethodPatch, resourcePath("/v1/webhook-subscriptions", id), "", request, &subscription)
	return subscription, err
}

func (c *Client) DeleteWebhookSubscription(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodDelete, resourcePath("/v1/webhook-subscriptions", id), "", nil, nil)
	return err
}

func (c *Client) ListWebhookDeliveries(ctx context.Context, options ListWebhookDeliveriesOptions) ([]WebhookDelivery, error) {
	endpoint := "/v1/webhook-deliveries"
	query := url.Values{}
	if options.Status != "" {
		query.Set("status", options.Status)
	}
	if options.SubscriptionID != "" {
		query.Set("subscription_id", options.SubscriptionID)
	}
	if options.Limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", options.Limit))
	}
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var response struct {
		Items []WebhookDelivery `json:"items"`
	}
	if _, err := c.do(ctx, http.MethodGet, endpoint, "", nil, &response); err != nil {
		return nil, err
	}
	if response.Items == nil {
		return []WebhookDelivery{}, nil
	}
	return response.Items, nil
}

func (c *Client) AttachEgressProfile(ctx context.Context, instanceID, profileID string) (Instance, error) {
	var instance Instance
	_, err := c.do(ctx, http.MethodPut, resourcePath("/v1/instances", instanceID, "network", "egress-profile"), "", map[string]any{
		"egress_profile_id": profileID,
	}, &instance)
	if err == nil {
		err = instance.validate()
	}
	return instance, err
}

func (c *Client) ListSuggestedPorts(ctx context.Context, instanceID string) ([]int, error) {
	var envelope struct {
		Items []int `json:"items"`
	}
	_, err := c.do(ctx, http.MethodGet, resourcePath("/v1/instances", instanceID, "suggested-ports"), "", nil, &envelope)
	if err != nil {
		return nil, err
	}
	if envelope.Items == nil {
		return []int{}, nil
	}
	return envelope.Items, nil
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
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	return request, nil
}

func (c *Client) artifactRequest(ctx context.Context, method, instanceID, artifactPath, suffix string, body io.Reader) (*http.Request, error) {
	target := *c.baseURL
	rawBasePath := strings.TrimSuffix(c.baseURL.EscapedPath(), "/")
	target.Path = path.Join(strings.TrimSuffix(c.baseURL.Path, "/"), "v1", "instances", instanceID, "artifacts", artifactPath, suffix)
	target.RawPath = rawBasePath + "/v1/instances/" + url.PathEscape(instanceID) + "/artifacts/" + url.PathEscape(artifactPath)
	if suffix != "" {
		target.RawPath += "/" + url.PathEscape(suffix)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set(APIVersionHeader, APIVersionV1)
	if c.userAgent != "" {
		request.Header.Set("User-Agent", c.userAgent)
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
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
