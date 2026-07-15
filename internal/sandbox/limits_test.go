// SPDX-License-Identifier: AGPL-3.0-only

package sandbox_test

import (
	"errors"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/execstream"
	"github.com/openbox-dev/openbox/internal/sandbox"
)

func TestExecGateLimitsConcurrentExecsPerInstance(t *testing.T) {
	t.Parallel()
	gate := sandbox.NewExecGate(1)
	release1, err := gate.Acquire("box-1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = gate.Acquire("box-1")
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeBusy {
		t.Fatalf("err=%v want busy", err)
	}
	_, err = gate.Acquire("box-2")
	if err != nil {
		t.Fatal(err)
	}
	release1()
	release2, err := gate.Acquire("box-1")
	if err != nil {
		t.Fatal(err)
	}
	release2()
}

func TestRateLimitedSinkEnforcesOutputBytes(t *testing.T) {
	t.Parallel()
	inner := &frameBuf{}
	sink := sandbox.NewRateLimitedSink(inner, 8, time.Hour, func() time.Time {
		return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	})
	if err := sink.Emit(execstream.StdoutFrame{Data: []byte("12345678")}); err != nil {
		t.Fatal(err)
	}
	err := sink.Emit(execstream.StdoutFrame{Data: []byte("x")})
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeRateLimited {
		t.Fatalf("err=%v want rate_limited", err)
	}
}
