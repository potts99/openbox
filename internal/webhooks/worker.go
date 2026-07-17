// SPDX-License-Identifier: AGPL-3.0-only

package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

type DeliveryRepository interface {
	ListClaimableWebhookDeliveries(context.Context, time.Time, int) ([]domain.WebhookDelivery, error)
	ClaimWebhookDelivery(context.Context, domain.WebhookDeliveryID, string, time.Time) (domain.WebhookDispatch, bool, error)
	CompleteWebhookDelivery(context.Context, domain.WebhookDispatch, int, string, time.Time) error
}

type Worker struct {
	repo       DeliveryRepository
	httpClient *http.Client
	workerID   string
	now        func() time.Time
}

func NewWorker(repo DeliveryRepository, httpClient *http.Client) (*Worker, error) {
	if repo == nil {
		return nil, errors.New("webhook delivery repository is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Worker{repo: repo, httpClient: httpClient, workerID: "openboxd-webhooks", now: time.Now}, nil
}

func (w *Worker) RunOnce(ctx context.Context) error {
	now := w.now().UTC()
	candidates, err := w.repo.ListClaimableWebhookDeliveries(ctx, now, 32)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	errCh := make(chan error, len(candidates))
	for _, candidate := range candidates {
		dispatch, claimed, err := w.repo.ClaimWebhookDelivery(ctx, candidate.ID, w.workerID, now)
		if err != nil {
			return err
		}
		if !claimed {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, class := w.deliver(ctx, dispatch)
			if err := w.repo.CompleteWebhookDelivery(ctx, dispatch, status, class, w.now().UTC()); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	var joined error
	for err := range errCh {
		joined = errors.Join(joined, err)
	}
	return joined
}

func (w *Worker) deliver(ctx context.Context, dispatch domain.WebhookDispatch) (int, string) {
	timestamp := strconv.FormatInt(w.now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(dispatch.Secret))
	_, _ = fmt.Fprintf(mac, "v1|%s|%s|%s|%s", timestamp, dispatch.EventID, dispatch.Delivery.ID, dispatch.Payload)
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, dispatch.URL, bytes.NewReader(dispatch.Payload))
	if err != nil {
		return 0, "connection"
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-OpenBox-Event-Id", dispatch.EventID)
	request.Header.Set("X-OpenBox-Delivery-Id", string(dispatch.Delivery.ID))
	request.Header.Set("X-OpenBox-Signature-Timestamp", timestamp)
	request.Header.Set("X-OpenBox-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
	response, err := w.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, "timeout"
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return 0, "timeout"
		}
		return 0, "connection"
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, "http_error"
	}
	return response.StatusCode, ""
}
