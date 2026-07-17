// SPDX-License-Identifier: AGPL-3.0-only

// Minimal agent-style server: create sandbox → exec → cleanup via pkg/openbox.
// Optionally registers a webhook subscription and verifies signed deliveries.
//
// Run from repo root:
//
//	export OPENBOX_TOKEN=…
//	go run ./examples/agent-server/
package main

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/execstream"
	openbox "github.com/openbox-dev/openbox/pkg/openbox"
)

func main() {
	ctx := context.Background()
	client, err := openbox.New(openbox.Options{
		BaseURL: envOr("OPENBOX_SERVER", openbox.DefaultBaseURL),
		Token:   os.Getenv("OPENBOX_TOKEN"),
	})
	if err != nil {
		log.Fatal(err)
	}
	if os.Getenv("OPENBOX_TOKEN") == "" {
		log.Fatal("OPENBOX_TOKEN is required")
	}
	if _, err := client.Negotiate(ctx); err != nil {
		log.Fatal(err)
	}

	subscriptionID, secret := maybeRegisterWebhook(ctx, client)
	if subscriptionID != "" {
		defer func() {
			if err := client.DeleteWebhookSubscription(ctx, subscriptionID); err != nil {
				log.Printf("delete webhook subscription: %v", err)
			}
		}()
		startWebhookListener(secret)
	}

	name := fmt.Sprintf("agent-server-%d", time.Now().Unix())
	idempotency := "create-" + name
	result, err := client.CreateInstance(ctx, openbox.CreateInstanceRequest{
		Name:            name,
		Kind:            "sandbox",
		Image:           "openbox:sandbox/ubuntu/24.04",
		Resources:       openbox.Resources{VCPUs: 1, MemoryBytes: 1 << 30, DiskBytes: 8 << 30},
		LifetimeSeconds: 1800,
		// EgressProfileID omitted → sandbox default egress-restricted
	}, idempotency)
	if err != nil {
		log.Fatal(err)
	}
	instanceID := result.Instance.ID
	createOp := result.Operation.ID
	log.Printf("created instance %s (operation %s)", instanceID, createOp)

	if err := waitOperation(ctx, client, createOp); err != nil {
		log.Fatal(err)
	}

	body, err := client.ExecInstance(ctx, instanceID, openbox.ExecInstanceRequest{
		Argv: []string{"uname", "-a"},
	})
	if err != nil {
		log.Fatal(err)
	}
	exitCode, err := drainExec(body, os.Stdout, os.Stderr)
	body.Close()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("exec exit code: %d", exitCode)

	deleteKey := "delete-" + name
	del, err := client.DeleteInstance(ctx, instanceID, deleteKey)
	if err != nil {
		log.Fatal(err)
	}
	if err := waitOperation(ctx, client, del.Operation.ID); err != nil {
		log.Fatal(err)
	}
	log.Printf("deleted instance %s", instanceID)

	if subscriptionID != "" {
		deliveries, err := client.ListWebhookDeliveries(ctx, openbox.ListWebhookDeliveriesOptions{
			SubscriptionID: subscriptionID,
			Limit:          20,
		})
		if err != nil {
			log.Printf("list webhook deliveries: %v", err)
		} else {
			log.Printf("webhook deliveries: %d", len(deliveries))
			for _, item := range deliveries {
				log.Printf("  %s status=%s event=%s", item.ID, item.Status, item.EventID)
			}
		}
	}
}

func maybeRegisterWebhook(ctx context.Context, client *openbox.Client) (id, secret string) {
	url := strings.TrimSpace(os.Getenv("OPENBOX_WEBHOOK_URL"))
	if url == "" {
		log.Print("webhook registration skipped (set OPENBOX_WEBHOOK_URL to enable)")
		return "", ""
	}
	enabled := true
	created, err := client.CreateWebhookSubscription(ctx, openbox.CreateWebhookSubscriptionRequest{
		URL:         url,
		Description: "examples/agent-server",
		Events:      []string{"operation.terminal", "instance.deleted"},
		Enabled:     &enabled,
	})
	if err != nil {
		log.Fatalf("create webhook subscription: %v", err)
	}
	log.Printf("registered webhook %s → %s", created.ID, created.URL)
	return created.ID, created.Secret
}

func waitOperation(ctx context.Context, client *openbox.Client, id string) error {
	for {
		op, err := client.GetOperation(ctx, id)
		if err != nil {
			return err
		}
		switch op.Status {
		case openbox.OperationSucceeded:
			return nil
		case openbox.OperationFailed:
			return fmt.Errorf("operation %s failed: %s", id, op.ErrorCode)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func drainExec(body io.Reader, stdout, stderr io.Writer) (int, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), execstream.MaxFrameBytes+1024)
	exitCode := 0
	for scanner.Scan() {
		frame, err := execstream.Decode(scanner.Bytes())
		if err != nil {
			return exitCode, err
		}
		switch f := frame.(type) {
		case execstream.StdoutFrame:
			_, _ = stdout.Write(f.Data)
		case execstream.StderrFrame:
			_, _ = stderr.Write(f.Data)
		case execstream.ExitFrame:
			exitCode = f.Code
		case execstream.ErrorFrame:
			return exitCode, fmt.Errorf("exec error: %s", f.Code)
		}
	}
	return exitCode, scanner.Err()
}

func webhookHandler(secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if !verifyWebhookSignature(secret, payload, r.Header) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		log.Printf("webhook delivery event=%s delivery=%s (%d bytes)",
			r.Header.Get("X-OpenBox-Event-Id"),
			r.Header.Get("X-OpenBox-Delivery-Id"),
			len(payload),
		)
	}
}

func verifyWebhookSignature(secret string, payload []byte, headers http.Header) bool {
	timestamp := headers.Get("X-OpenBox-Signature-Timestamp")
	eventID := headers.Get("X-OpenBox-Event-Id")
	deliveryID := headers.Get("X-OpenBox-Delivery-Id")
	header := headers.Get("X-OpenBox-Signature")
	const prefix = "v1="
	if timestamp == "" || eventID == "" || deliveryID == "" || !strings.HasPrefix(header, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "v1|%s|%s|%s|%s", timestamp, eventID, deliveryID, payload)
	return hmac.Equal(mac.Sum(nil), want)
}

func startWebhookListener(secret string) {
	if secret == "" {
		return
	}
	addr := envOr("OPENBOX_WEBHOOK_LISTEN", ":8787")
	go func() {
		log.Printf("webhook listener on %s", addr)
		if err := http.ListenAndServe(addr, webhookHandler(secret)); err != nil {
			log.Printf("webhook listener: %v", err)
		}
	}()
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
