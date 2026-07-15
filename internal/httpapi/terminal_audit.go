// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

// Terminal session audit phases. Only lifecycle metadata is recorded — never
// PTY input/output bytes or WebSocket frame payloads.
const (
	TerminalAuditPhaseStart = "start"
	TerminalAuditPhaseEnd   = "end"
)

// Terminal session end reasons (metadata only).
const (
	TerminalAuditReasonExit          = "exit"
	TerminalAuditReasonDetach        = "detach"
	TerminalAuditReasonIdleTimeout   = "idle_timeout"
	TerminalAuditReasonFrameTooLarge = "frame_too_large"
	TerminalAuditReasonRateLimited   = "rate_limited"
	TerminalAuditReasonError         = "error"
	TerminalAuditReasonCanceled      = "canceled"
)

// TerminalAuditEvent is lifecycle metadata for a browser terminal session.
// It must never carry input/output bytes or encoded frame payloads.
type TerminalAuditEvent struct {
	At          time.Time
	OwnerID     domain.OwnerID
	InstanceID  domain.InstanceID
	SessionID   string
	SessionName string
	Phase       string
	Reason      string
}

// TerminalAuditor records terminal start/end metadata. Implementations must not
// accept or persist PTY payloads.
type TerminalAuditor interface {
	Record(context.Context, TerminalAuditEvent) error
}

func (h *Handler) recordTerminalAudit(ctx context.Context, event TerminalAuditEvent) {
	if h.terminalAudit == nil {
		return
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	} else {
		event.At = event.At.UTC()
	}
	_ = h.terminalAudit.Record(ctx, event)
}
