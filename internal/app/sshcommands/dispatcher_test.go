// SPDX-License-Identifier: AGPL-3.0-only

package sshcommands

import (
	"bytes"
	"context"
	"testing"

	"github.com/openbox-dev/openbox/internal/app/clones"
	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
)

func TestDispatcherMapsTypedCommandsAndNeverRunsRejectedInput(t *testing.T) {
	service := &fakeService{values: []domain.Instance{{ID: "instance-1", OwnerID: "owner", Name: "dev", Kind: domain.KindVPS, ObservedState: domain.ObservedStopped}}}
	dispatcher, err := New(service, "ssh-ed25519 internal-instance-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.newKey = func() (string, error) { return "generated", nil }
	for _, test := range []struct {
		command string
		want    string
	}{
		{"ls", "dev"},
		{"inspect dev", "instance-1"},
		{"start dev", "operation"},
		{"new fresh --kind vps", "fresh"},
	} {
		var stdout, stderr bytes.Buffer
		if code := dispatcher.Execute(context.Background(), "owner", test.command, nil, &stdout, &stderr); code != 0 || !bytes.Contains(stdout.Bytes(), []byte(test.want)) {
			t.Fatalf("%q exit=%d stdout=%q stderr=%q", test.command, code, stdout.String(), stderr.String())
		}
	}
	calls := service.calls
	var stdout, stderr bytes.Buffer
	if code := dispatcher.Execute(context.Background(), "owner", "ls; touch /tmp/pwned", nil, &stdout, &stderr); code != 2 || service.calls != calls {
		t.Fatalf("injection exit=%d service calls=%d", code, service.calls)
	}
}

func TestDispatcherCopyUsesOnlyExplicitCopier(t *testing.T) {
	service := &fakeService{}
	dispatcher, _ := New(service, "ssh-ed25519 internal-instance-key", nil)
	var stdout, stderr bytes.Buffer
	if code := dispatcher.Execute(context.Background(), "owner", "cp base feature", nil, &stdout, &stderr); code != 1 || !bytes.Contains(stderr.Bytes(), []byte("unavailable")) {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestDispatcherCopyPrintsWarnings(t *testing.T) {
	service := &fakeService{values: []domain.Instance{{ID: "instance-1", OwnerID: "owner", Name: "base", Kind: domain.KindVPS}}}
	copier := &fakeCopier{warnings: []string{clones.WarningFullCopy, clones.WarningSecrets}}
	dispatcher, _ := New(service, "ssh-ed25519 internal-instance-key", copier)
	dispatcher.newKey = func() (string, error) { return "generated", nil }
	var stdout, stderr bytes.Buffer
	if code := dispatcher.Execute(context.Background(), "owner", "cp base feature", nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(clones.WarningFullCopy)) || !bytes.Contains(stdout.Bytes(), []byte(clones.WarningSecrets)) {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

type fakeCopier struct{ warnings []string }

func (c *fakeCopier) SubmitCopy(context.Context, domain.OwnerID, string, string, string) (clones.SubmitResult, error) {
	return clones.SubmitResult{
		Instance:  domain.Instance{ID: "clone-1", Name: "feature"},
		Operation: domain.Operation{ID: "op-copy", Status: domain.OperationPending},
		Warnings:  c.warnings,
	}, nil
}

type fakeService struct {
	values []domain.Instance
	calls  int
}

func (s *fakeService) ListInstances(context.Context, domain.OwnerID) ([]domain.Instance, error) {
	s.calls++
	return s.values, nil
}
func (s *fakeService) SubmitCreate(_ context.Context, input instances.CreateInput) (domain.Instance, domain.Operation, error) {
	s.calls++
	instance := domain.Instance{ID: "instance-new", OwnerID: input.OwnerID, Name: input.Name, Kind: input.Kind}
	return instance, domain.Operation{ID: "operation-new", Status: domain.OperationPending}, nil
}
func (s *fakeService) SubmitAction(_ context.Context, _ domain.OwnerID, _ domain.InstanceID, _ instances.MutationAction, _ string) (domain.Operation, error) {
	s.calls++
	return domain.Operation{ID: "operation-action", Status: domain.OperationPending}, nil
}
