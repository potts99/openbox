// SPDX-License-Identifier: AGPL-3.0-only

package build

import (
	"context"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/images"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

func TestRecipeCommandsUsePinnedPackageManagers(t *testing.T) {
	definition, err := images.LoadDevboxDefinition()
	if err != nil {
		t.Fatal(err)
	}
	commands := recipeCommands(definition)
	want := map[string]bool{
		"apt-get update": true,
		"DEBIAN_FRONTEND=noninteractive apt-get install -y 'nodejs=22.23.1-1nodesource1'": true,
		"DEBIAN_FRONTEND=noninteractive apt-get install -y 'tmux=3.4-1ubuntu0.1'":         true,
		"npm install -g '@earendil-works/pi-coding-agent@0.80.7'":                         true,
	}
	for _, command := range commands {
		delete(want, command)
	}
	if len(want) != 0 {
		t.Fatalf("missing translated commands: %v", want)
	}
}

func TestSubmitBuildUsesImageBuildTarget(t *testing.T) {
	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	repo := &fakeRepository{}
	service, err := New(fakeRuntime{capabilities: runtimeapi.Capabilities{Architecture: "x86_64", Containers: true}}, repo, Options{
		Now: func() time.Time { return now },
		NewID: func() string {
			if repo.calls == 0 {
				repo.calls++
				return "build-000000000000"
			}
			return "operation-0000000"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	op, err := service.Submit(context.Background(), Input{OwnerID: "owner", IdempotencyKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if op.Type != operationType || op.TargetType != "image" || op.TargetID != "build-000000000000" {
		t.Fatalf("operation=%+v", op)
	}
	if repo.build.Alias != "openbox:devbox/ubuntu/24.04" || repo.build.BuilderRef != "obx-build-build-0000000000" {
		t.Fatalf("build=%+v", repo.build)
	}
}

type fakeRuntime struct{ capabilities runtimeapi.Capabilities }

func (r fakeRuntime) DiscoverCapabilities(context.Context) (runtimeapi.Capabilities, error) {
	return r.capabilities, nil
}
func (fakeRuntime) CreateImageBuilder(context.Context, string, string, string, string) error {
	return nil
}
func (fakeRuntime) StartInstance(context.Context, string) error { return nil }
func (fakeRuntime) StopInstance(context.Context, string) error  { return nil }
func (fakeRuntime) Exec(context.Context, runtimeapi.ExecRequest) (runtimeapi.ExecResult, error) {
	return runtimeapi.ExecResult{}, nil
}
func (fakeRuntime) DeleteInstance(context.Context, string) error { return nil }
func (fakeRuntime) PublishImageAlias(context.Context, string, string) (string, error) {
	return "digest", nil
}

type fakeRepository struct {
	build domain.ImageBuild
	calls int
}

func (r *fakeRepository) CreateImageBuild(_ context.Context, build domain.ImageBuild, op domain.Operation) (domain.Operation, bool, error) {
	r.build = build
	return op, false, nil
}
func (r *fakeRepository) GetImageBuild(context.Context, domain.OwnerID, string) (domain.ImageBuild, error) {
	return r.build, nil
}
func (*fakeRepository) PublishImageBuild(context.Context, domain.OwnerID, string, string, time.Time) error {
	return nil
}
func (*fakeRepository) UpdateOperationStage(context.Context, domain.OwnerID, domain.OperationID, string, int, time.Time) error {
	return nil
}
func (*fakeRepository) AppendOperationEvent(context.Context, domain.OwnerID, domain.OperationID, string, string, []byte, time.Time) error {
	return nil
}
