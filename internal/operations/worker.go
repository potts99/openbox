// SPDX-License-Identifier: AGPL-3.0-only

// Package operations runs durable local operation claims and retries.
package operations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openbox-dev/openbox/internal/clock"
	"github.com/openbox-dev/openbox/internal/domain"
)

type ErrorClass string

const (
	Transient   ErrorClass = "transient"
	Correctable ErrorClass = "correctable"
	Integrity   ErrorClass = "integrity"
)

type ClassifiedError struct {
	Class ErrorClass
	Code  domain.ErrorCode
	Err   error
}

func (e *ClassifiedError) Error() string { return e.Err.Error() }
func (e *ClassifiedError) Unwrap() error { return e.Err }
func TransientError(code domain.ErrorCode, err error) error {
	return &ClassifiedError{Class: Transient, Code: code, Err: err}
}
func CorrectableError(code domain.ErrorCode, err error) error {
	return &ClassifiedError{Class: Correctable, Code: code, Err: err}
}
func IntegrityError(code domain.ErrorCode, err error) error {
	return &ClassifiedError{Class: Integrity, Code: code, Err: err}
}

type Classification struct {
	Class ErrorClass
	Code  domain.ErrorCode
}
type Classifier func(error) Classification

func DefaultClassifier(err error) Classification {
	var classified *ClassifiedError
	if errors.As(err, &classified) {
		return Classification{Class: classified.Class, Code: classified.Code}
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		if domainErr.Code == domain.CodeRuntimeMissing {
			return Classification{Class: Integrity, Code: domainErr.Code}
		}
		return Classification{Class: Correctable, Code: domainErr.Code}
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return Classification{Class: Transient, Code: "runtime_unavailable"}
	}
	return Classification{Class: Correctable, Code: "operation_failed"}
}

type Repository interface {
	ListClaimableOperations(context.Context, time.Time, int) ([]domain.Operation, error)
	ClaimOperation(context.Context, domain.OperationID, string, string, time.Time, time.Duration) (domain.Operation, bool, bool, error)
	RenewClaim(context.Context, domain.OperationID, string, string, time.Time, time.Duration) (bool, error)
	RetryOperation(context.Context, domain.OwnerID, domain.OperationID, string, string, string, domain.ErrorCode, string, time.Time, time.Time) error
	FailOperation(context.Context, domain.OwnerID, domain.OperationID, string, string, string, domain.ErrorCode, string, time.Time) error
	CompleteClaim(context.Context, domain.OwnerID, domain.OperationID, string, string, time.Time) (bool, error)
}
type Executor interface {
	Execute(context.Context, domain.Operation) error
}

type Mode struct{ degraded atomic.Bool }

func (m *Mode) Degraded() bool         { return m.degraded.Load() }
func (m *Mode) SetDegraded(value bool) { m.degraded.Store(value) }

type Config struct {
	WorkerID      string
	Concurrency   int
	Lease         time.Duration
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	MaxAttempts   int
	Clock         clock.Clock
	Classifier    Classifier
	Mode          *Mode
	NewClaimToken func() string
}
type Worker struct {
	repo     Repository
	executor Executor
	config   Config
}

func NewWorker(repo Repository, executor Executor, config Config) (*Worker, error) {
	if repo == nil || executor == nil {
		return nil, errors.New("operation repository and executor are required")
	}
	if config.WorkerID == "" {
		config.WorkerID = "openboxd-local"
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 2
	}
	if config.Lease <= 0 {
		config.Lease = time.Minute
	}
	if config.BaseBackoff <= 0 {
		config.BaseBackoff = time.Second
	}
	if config.MaxBackoff <= 0 {
		config.MaxBackoff = time.Minute
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 8
	}
	if config.Clock == nil {
		config.Clock = clock.Real{}
	}
	if config.Classifier == nil {
		config.Classifier = DefaultClassifier
	}
	if config.Mode == nil {
		config.Mode = &Mode{}
	}
	if config.NewClaimToken == nil {
		config.NewClaimToken = randomClaimToken
	}
	return &Worker{repo: repo, executor: executor, config: config}, nil
}

func (w *Worker) RunOnce(ctx context.Context) error {
	now := w.config.Clock.Now()
	candidates, err := w.repo.ListClaimableOperations(ctx, now, w.config.Concurrency)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	errCh := make(chan error, len(candidates))
	success := atomic.Bool{}
	transientFailure := atomic.Bool{}
	for _, candidate := range candidates {
		token := w.config.NewClaimToken()
		op, claimed, _, claimErr := w.repo.ClaimOperation(ctx, candidate.ID, w.config.WorkerID, token, now, w.config.Lease)
		if claimErr != nil {
			return claimErr
		}
		if !claimed {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			claim := Claim{OwnerID: op.OwnerID, OperationID: op.ID, WorkerID: w.config.WorkerID, Token: token}
			execCtx, cancel := context.WithCancel(WithClaim(ctx, claim))
			lost, renewalErr, stopRenewal := w.renewClaim(execCtx, cancel, op, token)
			runErr := w.executor.Execute(execCtx, op)
			cancel()
			stopRenewal()
			if lost.Load() {
				if err := renewalErr(); err != nil {
					errCh <- err
				}
				return
			}
			if runErr != nil {
				classification := w.config.Classifier(runErr)
				if classification.Class == Transient {
					transientFailure.Store(true)
					w.config.Mode.SetDegraded(true)
				}
				if classification.Class == Transient && op.Attempts < w.config.MaxAttempts {
					next := w.config.Clock.Now().Add(w.backoff(op.Attempts))
					if retryErr := w.repo.RetryOperation(ctx, op.OwnerID, op.ID, w.config.WorkerID, token, string(classification.Class), classification.Code, runErr.Error(), next, w.config.Clock.Now()); retryErr != nil {
						errCh <- retryErr
					}
					return
				}
				if failErr := w.repo.FailOperation(ctx, op.OwnerID, op.ID, w.config.WorkerID, token, string(classification.Class), classification.Code, runErr.Error(), w.config.Clock.Now()); failErr != nil {
					errCh <- failErr
				}
				return
			}
			if _, completeErr := w.repo.CompleteClaim(ctx, op.OwnerID, op.ID, w.config.WorkerID, token, w.config.Clock.Now()); completeErr != nil {
				errCh <- completeErr
				return
			}
			success.Store(true)
		}()
	}
	wg.Wait()
	close(errCh)
	if success.Load() && !transientFailure.Load() {
		w.config.Mode.SetDegraded(false)
	}
	var joined error
	for item := range errCh {
		joined = errors.Join(joined, item)
	}
	return joined
}

func (w *Worker) renewClaim(ctx context.Context, cancel context.CancelFunc, op domain.Operation, token string) (*atomic.Bool, func() error, func()) {
	lost := &atomic.Bool{}
	var mu sync.Mutex
	var renewalErr error
	done := make(chan struct{})
	finished := make(chan struct{})
	interval := w.config.Lease / 3
	if interval <= 0 {
		interval = time.Millisecond
	}
	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-w.config.Clock.After(interval):
				ok, err := w.repo.RenewClaim(ctx, op.ID, w.config.WorkerID, token, w.config.Clock.Now(), w.config.Lease)
				if err != nil || !ok {
					lost.Store(true)
					mu.Lock()
					renewalErr = err
					mu.Unlock()
					cancel()
					return
				}
			}
		}
	}()
	readErr := func() error { mu.Lock(); defer mu.Unlock(); return renewalErr }
	stop := func() { close(done); <-finished }
	return lost, readErr, stop
}

func randomClaimToken() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(fmt.Sprintf("generate claim token: %v", err))
	}
	return hex.EncodeToString(value[:])
}

func (w *Worker) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	value := w.config.BaseBackoff
	for n := 1; n < attempt; n++ {
		if value >= w.config.MaxBackoff/2 {
			return w.config.MaxBackoff
		}
		value *= 2
	}
	if value > w.config.MaxBackoff {
		return w.config.MaxBackoff
	}
	return value
}

func (c Classification) String() string { return fmt.Sprintf("%s/%s", c.Class, c.Code) }
