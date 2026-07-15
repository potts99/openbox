// SPDX-License-Identifier: AGPL-3.0-only

package sandbox_test

import (
	"context"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/clock"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/sandbox"
)

func TestExpiredInstancesSelectsDueSandboxes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	instances := []domain.Instance{
		{ID: "due", Kind: domain.KindSandbox, DesiredState: domain.DesiredRunning, ExpiresAt: &past},
		{ID: "later", Kind: domain.KindSandbox, DesiredState: domain.DesiredRunning, ExpiresAt: &future},
		{ID: "vps", Kind: domain.KindVPS, DesiredState: domain.DesiredRunning},
		{ID: "already", Kind: domain.KindSandbox, DesiredState: domain.DesiredDeleted, ExpiresAt: &past},
		{ID: "deleting", Kind: domain.KindSandbox, DesiredState: domain.DesiredRunning, ObservedState: domain.ObservedDeleting, ExpiresAt: &past},
	}
	got := sandbox.ExpiredInstances(instances, now)
	if len(got) != 1 || got[0].ID != "due" {
		t.Fatalf("got=%v", got)
	}
}

func TestSchedulerMarksExpiredDesiredDeleted(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Second)
	clk := clock.NewFake(now)
	repo := &expiryRepo{instances: []domain.Instance{{
		ID: "box-1", OwnerID: "owner-1", Kind: domain.KindSandbox,
		DesiredState: domain.DesiredRunning, ObservedState: domain.ObservedRunning,
		ExpiresAt: &past,
	}}}
	expirer := &expiryRecorder{repo: repo}
	sched, err := sandbox.NewExpiryScheduler(repo, expirer, sandbox.ExpiryOptions{Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	n, err := sched.RunOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(expirer.calls) != 1 || expirer.calls[0] != "owner-1/box-1" {
		t.Fatalf("calls=%v", expirer.calls)
	}
	if repo.instances[0].DesiredState != domain.DesiredDeleted {
		t.Fatalf("desired=%q", repo.instances[0].DesiredState)
	}
	n, err = sched.RunOnce(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("second sweep n=%d err=%v", n, err)
	}
}

type expiryRepo struct {
	instances []domain.Instance
}

func (r *expiryRepo) ListInstances(context.Context) ([]domain.Instance, error) {
	out := make([]domain.Instance, len(r.instances))
	copy(out, r.instances)
	return out, nil
}

type expiryRecorder struct {
	repo  *expiryRepo
	calls []string
}

func (e *expiryRecorder) MarkExpired(_ context.Context, owner domain.OwnerID, id domain.InstanceID) error {
	e.calls = append(e.calls, string(owner)+"/"+string(id))
	for i := range e.repo.instances {
		if e.repo.instances[i].ID == id && e.repo.instances[i].OwnerID == owner {
			e.repo.instances[i].DesiredState = domain.DesiredDeleted
		}
	}
	return nil
}
