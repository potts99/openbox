// SPDX-License-Identifier: AGPL-3.0-only

// Package build executes the checked-in Devbox recipe as durable operations.
package build

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/images"
	"github.com/openbox-dev/openbox/internal/operations"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

const operationType = "image.build"

type Runtime interface {
	DiscoverCapabilities(context.Context) (runtimeapi.Capabilities, error)
	CreateImageBuilder(context.Context, string, string, string, string) error
	StartInstance(context.Context, string) error
	StopInstance(context.Context, string) error
	Exec(context.Context, runtimeapi.ExecRequest) (runtimeapi.ExecResult, error)
	DeleteInstance(context.Context, string) error
	PublishImageAlias(context.Context, string, string) (string, error)
}

type Repository interface {
	CreateImageBuild(context.Context, domain.ImageBuild, domain.Operation) (domain.Operation, bool, error)
	GetImageBuild(context.Context, domain.OwnerID, string) (domain.ImageBuild, error)
	PublishImageBuild(context.Context, domain.OwnerID, string, string, time.Time) error
	UpdateOperationStage(context.Context, domain.OwnerID, domain.OperationID, string, int, time.Time) error
	AppendOperationEvent(context.Context, domain.OwnerID, domain.OperationID, string, string, []byte, time.Time) error
}

type Options struct {
	Now   func() time.Time
	NewID func() string
}

type Service struct {
	runtime Runtime
	repo    Repository
	now     func() time.Time
	newID   func() string
}

type Input struct {
	OwnerID        domain.OwnerID
	Architecture   string
	Runtime        string
	IdempotencyKey string
}

func New(runtime Runtime, repo Repository, options Options) (*Service, error) {
	if runtime == nil || repo == nil {
		return nil, errors.New("image build runtime and repository are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = randomID
	}
	return &Service{runtime: runtime, repo: repo, now: options.Now, newID: options.NewID}, nil
}

// Submit validates the supported curated target and records no external work.
func (s *Service) Submit(ctx context.Context, input Input) (domain.Operation, error) {
	if input.OwnerID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 255 {
		return domain.Operation{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "idempotency_key"}
	}
	capabilities, err := s.runtime.DiscoverCapabilities(ctx)
	if err != nil {
		return domain.Operation{}, fmt.Errorf("discover runtime capabilities: %w", err)
	}
	if input.Architecture == "" {
		input.Architecture = capabilities.Architecture
	}
	if input.Runtime == "" {
		input.Runtime = "container"
	}
	if input.Architecture != "x86_64" && input.Architecture != "aarch64" {
		return domain.Operation{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "architecture"}
	}
	if capabilities.Architecture == "" || input.Architecture != capabilities.Architecture {
		return domain.Operation{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "architecture"}
	}
	if input.Runtime != "container" && input.Runtime != "virtual-machine" {
		return domain.Operation{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "runtime"}
	}
	if input.Runtime == "container" && !capabilities.Containers {
		return domain.Operation{}, &domain.Error{Code: domain.CodeUnavailable, Field: "runtime"}
	}
	if input.Runtime == "virtual-machine" && (!capabilities.VirtualMachines || capabilities.VMAvailability != runtimeapi.VMAvailable) {
		return domain.Operation{}, &domain.Error{Code: domain.CodeUnavailable, Field: "runtime"}
	}
	manifest, err := devboxManifest(input.Architecture, input.Runtime)
	if err != nil {
		return domain.Operation{}, err
	}
	hash, err := requestHash(input.Architecture, input.Runtime)
	if err != nil {
		return domain.Operation{}, err
	}
	now := s.now().UTC()
	buildID := s.newID()
	op := domain.Operation{
		ID: domain.OperationID(s.newID()), OwnerID: input.OwnerID, Type: operationType, TargetType: "image", TargetID: buildID,
		Status: domain.OperationPending, Stage: "runtime", IdempotencyKey: input.IdempotencyKey, RequestHash: hash, CreatedAt: now, UpdatedAt: now,
	}
	payload, err := json.Marshal(struct {
		Architecture string `json:"architecture"`
		Runtime      string `json:"runtime"`
		Alias        string `json:"alias"`
	}{input.Architecture, input.Runtime, manifest.Alias})
	if err != nil {
		return domain.Operation{}, err
	}
	op.PayloadJSON = payload
	created, replay, err := s.repo.CreateImageBuild(ctx, domain.ImageBuild{
		ID: buildID, OwnerID: input.OwnerID, Architecture: input.Architecture, Runtime: input.Runtime, Alias: manifest.Alias,
		BuilderRef: "obx-build-" + buildID[:16], CreatedAt: now, UpdatedAt: now,
	}, op)
	if err != nil {
		return domain.Operation{}, err
	}
	if replay {
		return created, nil
	}
	return op, nil
}

func devboxManifest(architecture, runtime string) (images.Manifest, error) {
	for _, manifest := range images.DefaultCatalog().List() {
		if manifest.Name == "devbox" && manifest.Architecture == architecture && manifest.Runtime == runtime {
			return manifest, nil
		}
	}
	return images.Manifest{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "build_target"}
}

// RecoverOperation makes repeated attempts safe: the builder identity is
// durable and publishing occurs only after setup and verification pass.
func (s *Service) RecoverOperation(ctx context.Context, operation domain.Operation) error {
	if operation.Type != operationType || operation.TargetType != "image" {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "operation.type"}
	}
	build, err := s.repo.GetImageBuild(ctx, operation.OwnerID, operation.TargetID)
	if err != nil {
		return err
	}
	if build.Digest != "" {
		return nil
	}
	definition, err := images.LoadDevboxDefinition()
	if err != nil {
		return operations.IntegrityError(domain.CodeInvalidArgument, err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	if err := s.stage(runCtx, operation, "preparing_builder", 10); err != nil {
		return err
	}
	if err := s.runtime.CreateImageBuilder(runCtx, build.BuilderRef, definition.Base, build.Architecture, build.Runtime); err != nil && !errors.Is(err, runtimeapi.ErrAlreadyExists) {
		return runtimeFailure("create builder", err)
	}
	if err := s.runtime.StartInstance(runCtx, build.BuilderRef); err != nil {
		return runtimeFailure("start builder", err)
	}
	if err := s.stage(runCtx, operation, "running_setup", 25); err != nil {
		return err
	}
	for _, command := range recipeCommands(definition) {
		if err := s.execAndLog(runCtx, operation, "running_setup", build.BuilderRef, command); err != nil {
			return err
		}
	}
	if err := s.stage(runCtx, operation, "running_verify", 75); err != nil {
		return err
	}
	for _, command := range definition.Verify {
		if err := s.execAndLog(runCtx, operation, "running_verify", build.BuilderRef, command); err != nil {
			return err
		}
	}
	if err := s.runtime.StopInstance(runCtx, build.BuilderRef); err != nil {
		return runtimeFailure("stop builder", err)
	}
	if err := s.stage(runCtx, operation, "publishing_alias", 90); err != nil {
		return err
	}
	digest, err := s.runtime.PublishImageAlias(runCtx, build.BuilderRef, build.Alias)
	if err != nil {
		return runtimeFailure("publish image", err)
	}
	if err := s.repo.PublishImageBuild(runCtx, operation.OwnerID, build.ID, digest, s.now().UTC()); err != nil {
		return err
	}
	if err := s.stage(runCtx, operation, "published", 99); err != nil {
		return err
	}
	_ = s.runtime.DeleteInstance(context.Background(), build.BuilderRef)
	return nil
}

func recipeCommands(definition images.DevboxDefinition) []string {
	commands := append([]string(nil), definition.Setup...)
	for _, pin := range definition.Packages {
		switch pin.Manager {
		case "apt":
			commands = append(commands, "DEBIAN_FRONTEND=noninteractive apt-get install -y "+shellQuote(pin.Name+"="+pin.Version))
		case "npm":
			commands = append(commands, "npm install -g "+shellQuote(pin.Name+"@"+pin.Version))
		}
	}
	return commands
}

func (s *Service) execAndLog(ctx context.Context, operation domain.Operation, stage, builderRef, command string) error {
	result, err := s.runtime.Exec(ctx, runtimeapi.ExecRequest{Ref: builderRef, Command: []string{"sh", "-lc", command}})
	if err != nil {
		return runtimeFailure("run "+command, err)
	}
	if err := s.log(ctx, operation, stage, "stdout", result.Stdout); err != nil {
		return err
	}
	if err := s.log(ctx, operation, stage, "stderr", result.Stderr); err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return operations.CorrectableError(domain.CodeRuntimeError, fmt.Errorf("%s exited with status %d", command, result.ExitCode))
	}
	return nil
}

func (s *Service) log(ctx context.Context, operation domain.Operation, stage, stream string, output []byte) error {
	metadata, err := json.Marshal(map[string]string{"stream": stream})
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		for len(line) > 16<<10 {
			if err := s.repo.AppendOperationEvent(ctx, operation.OwnerID, operation.ID, stage, line[:16<<10], metadata, s.now().UTC()); err != nil {
				return err
			}
			line = line[16<<10:]
		}
		if line != "" {
			if err := s.repo.AppendOperationEvent(ctx, operation.OwnerID, operation.ID, stage, line, metadata, s.now().UTC()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) stage(ctx context.Context, operation domain.Operation, stage string, progress int) error {
	return s.repo.UpdateOperationStage(ctx, operation.OwnerID, operation.ID, stage, progress, s.now().UTC())
}

func runtimeFailure(action string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return operations.CorrectableError(domain.ErrorCode("timeout"), fmt.Errorf("%s: %w", action, err))
	}
	return operations.CorrectableError(domain.CodeRuntimeError, fmt.Errorf("%s: %w", action, err))
}

func requestHash(architecture, runtime string) (string, error) {
	encoded, err := json.Marshal(struct {
		Architecture string `json:"architecture"`
		Runtime      string `json:"runtime"`
	}{architecture, runtime})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func randomID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return hex.EncodeToString(value)
}
