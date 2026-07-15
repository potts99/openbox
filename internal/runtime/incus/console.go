// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"context"
	"fmt"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

// OpenConsole opens an interactive PTY inside a managed Incus instance.
// The HTTP unix client does not yet speak Incus exec websockets, so interactive
// consoles return ErrUnsupported after rejecting host targets. Callers must not
// fall back to a host shell.
func (a *Adapter) OpenConsole(ctx context.Context, request runtimeapi.ConsoleRequest) (runtimeapi.ConsoleSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runtimeapi.IsHostConsoleTarget(request.Ref) {
		return nil, runtimeapi.ErrHostTarget
	}
	if request.Ref == "" {
		return nil, runtimeapi.ErrHostTarget
	}
	return nil, fmt.Errorf("incus interactive console over unix HTTP: %w", runtimeapi.ErrUnsupported)
}

var _ runtimeapi.ConsoleOpener = (*Adapter)(nil)
