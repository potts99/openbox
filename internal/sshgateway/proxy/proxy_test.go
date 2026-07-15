// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	"golang.org/x/crypto/ssh"
)

func TestEnsureReadySubmitsDurableStartAndWaits(t *testing.T) {
	service := &fakeService{instance: domain.Instance{ID: "instance-1", OwnerID: "owner", Name: "dev", RuntimeRef: "obx-instance-1", DesiredState: domain.DesiredStopped, ObservedState: domain.ObservedStopped}}
	proxy, err := New(service, fakeAddresses{}, signer(t), Options{PollInterval: time.Millisecond, HostKey: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	var progress bytes.Buffer
	target, err := proxy.EnsureReady(context.Background(), "owner", "dev", &progress)
	if err != nil {
		t.Fatal(err)
	}
	if service.actions != 1 || target.Ref != "obx-instance-1" || !bytes.Contains(progress.Bytes(), []byte("operation-1")) {
		t.Fatalf("actions=%d target=%+v progress=%q", service.actions, target, progress.String())
	}
}

func TestEnsureReadyHonorsBoundedContextAndFailedOperation(t *testing.T) {
	service := &fakeService{instance: domain.Instance{ID: "instance-1", OwnerID: "owner", Name: "dev", RuntimeRef: "ref", DesiredState: domain.DesiredStopped, ObservedState: domain.ObservedStopped}, fail: true}
	proxy, _ := New(service, fakeAddresses{}, signer(t), Options{PollInterval: time.Millisecond, HostKey: ssh.InsecureIgnoreHostKey()})
	if _, err := proxy.EnsureReady(context.Background(), "owner", "dev", &bytes.Buffer{}); err == nil {
		t.Fatal("failed operation accepted")
	}
	service.fail = false
	service.never = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := proxy.EnsureReady(ctx, "owner", "dev", &bytes.Buffer{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error=%v", err)
	}
}

type fakeService struct {
	instance       domain.Instance
	actions, reads int
	fail, never    bool
}

func (s *fakeService) ListInstances(context.Context, domain.OwnerID) ([]domain.Instance, error) {
	return []domain.Instance{s.instance}, nil
}
func (s *fakeService) GetInstance(context.Context, domain.OwnerID, domain.InstanceID) (domain.Instance, error) {
	s.reads++
	if !s.never && !s.fail && s.reads > 1 {
		s.instance.DesiredState = domain.DesiredRunning
		s.instance.ObservedState = domain.ObservedRunning
	}
	return s.instance, nil
}
func (s *fakeService) SubmitAction(context.Context, domain.OwnerID, domain.InstanceID, instances.MutationAction, string) (domain.Operation, error) {
	s.actions++
	return domain.Operation{ID: "operation-1", Status: domain.OperationPending}, nil
}
func (s *fakeService) GetOperation(context.Context, domain.OwnerID, domain.OperationID) (domain.Operation, error) {
	if s.fail {
		return domain.Operation{ID: "operation-1", Status: domain.OperationFailed, ErrorCode: domain.CodeUnavailable}, nil
	}
	if !s.never && s.reads > 1 {
		return domain.Operation{ID: "operation-1", Status: domain.OperationSucceeded}, nil
	}
	return domain.Operation{ID: "operation-1", Status: domain.OperationRunning}, nil
}

type fakeAddresses struct{}

func (fakeAddresses) InstanceSSHAddress(context.Context, string) (string, error) {
	return "127.0.0.1", nil
}
func signer(t *testing.T) ssh.Signer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	value, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
