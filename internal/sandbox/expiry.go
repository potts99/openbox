// SPDX-License-Identifier: AGPL-3.0-only

package sandbox

import (
	"context"
	"errors"
	"time"

	"github.com/openbox-dev/openbox/internal/clock"
	"github.com/openbox-dev/openbox/internal/domain"
)

// InstanceLister returns durable instances for expiry sweeps.
type InstanceLister interface {
	ListInstances(context.Context) ([]domain.Instance, error)
}

// Expirer marks a due Sandbox for deletion (desired state deleted).
type Expirer interface {
	MarkExpired(context.Context, domain.OwnerID, domain.InstanceID) error
}

// ExpiryOptions configures the durable Sandbox expiry sweeper.
type ExpiryOptions struct {
	Clock clock.Clock
}

// ExpiryScheduler sweeps stored expires_at timestamps and requests deletion.
type ExpiryScheduler struct {
	repo    InstanceLister
	expirer Expirer
	clock   clock.Clock
}

// NewExpiryScheduler builds a sweeper driven by stored UTC timestamps.
func NewExpiryScheduler(repo InstanceLister, expirer Expirer, options ExpiryOptions) (*ExpiryScheduler, error) {
	if repo == nil || expirer == nil {
		return nil, errors.New("repo and expirer are required")
	}
	if options.Clock == nil {
		options.Clock = clock.Real{}
	}
	return &ExpiryScheduler{repo: repo, expirer: expirer, clock: options.Clock}, nil
}

// ExpiredInstances returns Sandboxes whose expires_at is at or before now and
// that are not already on an irreversible delete path.
func ExpiredInstances(instances []domain.Instance, now time.Time) []domain.Instance {
	now = now.UTC()
	var out []domain.Instance
	for _, instance := range instances {
		if instance.Kind != domain.KindSandbox || instance.ExpiresAt == nil {
			continue
		}
		if instance.DesiredState == domain.DesiredDeleted {
			continue
		}
		if instance.ObservedState == domain.ObservedDeleting || instance.ObservedState == domain.ObservedDeleted {
			continue
		}
		if instance.ExpiresAt.After(now) {
			continue
		}
		out = append(out, instance)
	}
	return out
}

// RunOnce lists instances, finds due Sandboxes, and marks each expired.
// It returns how many instances were marked.
func (s *ExpiryScheduler) RunOnce(ctx context.Context) (int, error) {
	instances, err := s.repo.ListInstances(ctx)
	if err != nil {
		return 0, err
	}
	due := ExpiredInstances(instances, s.clock.Now())
	for i, instance := range due {
		if err := s.expirer.MarkExpired(ctx, instance.OwnerID, instance.ID); err != nil {
			return i, err
		}
	}
	return len(due), nil
}
