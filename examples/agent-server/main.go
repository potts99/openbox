// SPDX-License-Identifier: AGPL-3.0-only

// Minimal agent-style server: create sandbox → exec → cleanup via pkg/openbox.
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

	// TODO(Slice E): when pkg/openbox exposes webhook subscription CRUD, register
	// a subscription and drive readiness from signed POSTs instead of polling.
	_ = startWebhookStub()
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

// webhookStubHandler demonstrates verifying X-OpenBox-Signature once webhooks ship.
// Not started by default — call startWebhookStub when integrating Slice E.
func webhookStubHandler(secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		sig := r.Header.Get("X-OpenBox-Signature")
		payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if secret != "" && !verifyWebhookSignature(secret, payload, sig) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		log.Printf("webhook delivery (%d bytes)", len(payload))
	}
}

func verifyWebhookSignature(secret string, payload []byte, header string) bool {
	// Expected format documented in docs/api/webhooks.md (sha256=…).
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return hmac.Equal(mac.Sum(nil), want)
}

func startWebhookStub() error {
	secret := os.Getenv("OPENBOX_WEBHOOK_SECRET")
	if secret == "" {
		log.Print("webhook stub not started (set OPENBOX_WEBHOOK_SECRET to enable listener)")
		return nil
	}
	addr := envOr("OPENBOX_WEBHOOK_LISTEN", ":8787")
	go func() {
		log.Printf("webhook stub listening on %s (TODO: register subscription via SDK)", addr)
		if err := http.ListenAndServe(addr, webhookStubHandler(secret)); err != nil {
			log.Printf("webhook stub: %v", err)
		}
	}()
	return nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
