// SPDX-License-Identifier: AGPL-3.0-only

package reuse

import (
	"context"
	"fmt"
	"strings"
	"time"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// FileWriter is the narrow runtime surface needed to re-personalize SSH access.
type FileWriter interface {
	WriteFile(context.Context, runtimeapi.WriteFileRequest) error
	StartInstance(context.Context, string) error
	StopInstance(context.Context, string) error
	InspectInstance(context.Context, string) (runtimeapi.Instance, error)
}

// AuthorizedKeys combines owner access with the daemon's instance gateway key.
func AuthorizedKeys(owner, gateway string) string {
	owner, gateway = strings.TrimSpace(owner), strings.TrimSpace(gateway)
	if gateway == "" || gateway == owner {
		return owner
	}
	if owner == "" {
		return gateway
	}
	return owner + "\n" + gateway
}

// WriteOwnerAuthorizedKeys rewrites OpenBox-managed owner and gateway SSH
// access on a derived instance. It does not claim to remove other guest
// credentials.
func WriteOwnerAuthorizedKeys(ctx context.Context, runtime FileWriter, ref, ownerPublicKey, gatewayPublicKey string) error {
	ownerPublicKey = AuthorizedKeys(ownerPublicKey, gatewayPublicKey)
	if ref == "" || ownerPublicKey == "" {
		return fmt.Errorf("personalize: ref and owner public key are required")
	}
	var body strings.Builder
	for _, line := range strings.Split(ownerPublicKey, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	payload := body.String()
	inspected, inspectErr := runtime.InspectInstance(ctx, ref)
	if inspectErr != nil {
		return fmt.Errorf("inspect before personalize start: %w", inspectErr)
	}
	wasRunning := inspected.State == runtimeapi.StateRunning
	if !wasRunning {
		if startErr := runtime.StartInstance(ctx, ref); startErr != nil {
			return fmt.Errorf("start for personalize: %w", startErr)
		}
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var writeErr error
	for {
		writeErr = runtime.WriteFile(writeCtx, runtimeapi.WriteFileRequest{
			Ref: ref, Path: "/root/.ssh/authorized_keys", Body: strings.NewReader(payload), Mode: 0o600,
		})
		if writeErr == nil || writeCtx.Err() != nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if writeErr != nil {
		if !wasRunning {
			_ = runtime.StopInstance(ctx, ref)
		}
		return fmt.Errorf("write owner authorized_keys: %w", writeErr)
	}
	if !wasRunning {
		if stopErr := runtime.StopInstance(ctx, ref); stopErr != nil {
			return fmt.Errorf("stop after personalize: %w", stopErr)
		}
	}
	return nil
}
