// SPDX-License-Identifier: AGPL-3.0-only

// Package sshcommands adapts the typed SSH command protocol to OpenBox's
// application services. It contains no runtime or host-shell access.
package sshcommands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/openbox-dev/openbox/internal/app/clones"
	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/sshgateway/commands"
)

type Service interface {
	ListInstances(context.Context, domain.OwnerID) ([]domain.Instance, error)
	SubmitCreate(context.Context, instances.CreateInput) (domain.Instance, domain.Operation, error)
	SubmitAction(context.Context, domain.OwnerID, domain.InstanceID, instances.MutationAction, string) (domain.Operation, error)
}

type Copier interface {
	SubmitCopy(context.Context, domain.OwnerID, string, string, string) (clones.SubmitResult, error)
}

type Dispatcher struct {
	service           Service
	instancePublicKey string
	copier            Copier
	newKey            func() (string, error)
}

func New(service Service, instancePublicKey string, copier Copier) (*Dispatcher, error) {
	if service == nil || strings.TrimSpace(instancePublicKey) == "" {
		return nil, errors.New("SSH command service and internal instance key are required")
	}
	return &Dispatcher{service: service, instancePublicKey: strings.TrimSpace(instancePublicKey), copier: copier, newKey: randomKey}, nil
}

func (d *Dispatcher) Execute(ctx context.Context, owner domain.OwnerID, raw string, _ io.Reader, stdout, stderr io.Writer) int {
	command, err := commands.Parse(raw)
	if err != nil {
		fmt.Fprintln(stderr, "invalid OpenBox command")
		return 2
	}
	if err := d.execute(ctx, owner, command, stdout); err != nil {
		fmt.Fprintf(stderr, "openbox: %s\n", safeError(err))
		return 1
	}
	return 0
}

func (d *Dispatcher) execute(ctx context.Context, owner domain.OwnerID, command commands.Command, output io.Writer) error {
	switch value := command.(type) {
	case commands.New:
		key, err := d.idempotency(value.IdempotencyKey)
		if err != nil {
			return err
		}
		instance, operation, err := d.service.SubmitCreate(ctx, instances.CreateInput{OwnerID: owner, Name: value.InstanceName, Kind: value.Kind, Image: value.Image, RequestedIsolation: value.Isolation, Resources: value.Resources, OwnerPublicKey: d.instancePublicKey, IdempotencyKey: key})
		if err != nil {
			return err
		}
		return writeResult(output, value.JSON, instance, operation)
	case commands.List:
		values, err := d.service.ListInstances(ctx, owner)
		if err != nil {
			return err
		}
		if value.JSON {
			return json.NewEncoder(output).Encode(struct {
				Items []domain.Instance `json:"items"`
			}{Items: values})
		}
		for _, instance := range values {
			fmt.Fprintf(output, "%s\t%s\t%s\n", instance.Name, instance.Kind, instance.ObservedState)
		}
		return nil
	case commands.Inspect:
		instance, err := d.resolve(ctx, owner, value.Target)
		if err != nil {
			return err
		}
		if value.JSON {
			return json.NewEncoder(output).Encode(instance)
		}
		fmt.Fprintf(output, "Name: %s\nID: %s\nKind: %s\nState: %s\nIsolation: %s\n", instance.Name, instance.ID, instance.Kind, instance.ObservedState, instance.ActualIsolation)
		return nil
	case commands.Start:
		return d.lifecycle(ctx, owner, value.Target, instances.MutationStart, value.IdempotencyKey, value.JSON, output)
	case commands.Stop:
		return d.lifecycle(ctx, owner, value.Target, instances.MutationStop, value.IdempotencyKey, value.JSON, output)
	case commands.Restart:
		return d.lifecycle(ctx, owner, value.Target, instances.MutationRestart, value.IdempotencyKey, value.JSON, output)
	case commands.Remove:
		return d.lifecycle(ctx, owner, value.Target, instances.MutationDelete, value.IdempotencyKey, value.JSON, output)
	case commands.Copy:
		if d.copier == nil {
			return errors.New("copy is unavailable until instance cloning is configured")
		}
		key, err := d.idempotency(value.IdempotencyKey)
		if err != nil {
			return err
		}
		result, err := d.copier.SubmitCopy(ctx, owner, value.Source, value.Destination, key)
		if err != nil {
			return err
		}
		for _, warning := range result.Warnings {
			fmt.Fprintf(output, "warning\t%s\n", warning)
		}
		return writeResult(output, value.JSON, result.Instance, result.Operation)
	default:
		return errors.New("unsupported OpenBox command")
	}
}

func (d *Dispatcher) lifecycle(ctx context.Context, owner domain.OwnerID, target string, action instances.MutationAction, supplied string, jsonOutput bool, output io.Writer) error {
	instance, err := d.resolve(ctx, owner, target)
	if err != nil {
		return err
	}
	key, err := d.idempotency(supplied)
	if err != nil {
		return err
	}
	operation, err := d.service.SubmitAction(ctx, owner, instance.ID, action, key)
	if err != nil {
		return err
	}
	return writeOperation(output, jsonOutput, operation)
}

func (d *Dispatcher) resolve(ctx context.Context, owner domain.OwnerID, target string) (domain.Instance, error) {
	instances, err := d.service.ListInstances(ctx, owner)
	if err != nil {
		return domain.Instance{}, err
	}
	for _, instance := range instances {
		if string(instance.ID) == target || instance.Name == target {
			return instance, nil
		}
	}
	return domain.Instance{}, &domain.Error{Code: domain.CodeNotFound, Field: "instance"}
}

func (d *Dispatcher) idempotency(value string) (string, error) {
	if value != "" {
		return value, nil
	}
	return d.newKey()
}

func randomKey() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "ssh-" + hex.EncodeToString(value), nil
}

func writeResult(output io.Writer, jsonOutput bool, instance domain.Instance, operation domain.Operation) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(struct {
			Instance  domain.Instance  `json:"instance"`
			Operation domain.Operation `json:"operation"`
		}{Instance: instance, Operation: operation})
	}
	fmt.Fprintf(output, "%s\t%s\noperation\t%s\n", instance.Name, instance.ID, operation.ID)
	return nil
}

func writeOperation(output io.Writer, jsonOutput bool, operation domain.Operation) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(operation)
	}
	fmt.Fprintf(output, "operation\t%s\t%s\n", operation.ID, operation.Status)
	return nil
}

func safeError(err error) string {
	var domainError *domain.Error
	if errors.As(err, &domainError) {
		if domainError.Field != "" {
			return strings.ReplaceAll(string(domainError.Code)+": "+domainError.Field, "\n", " ")
		}
		return string(domainError.Code)
	}
	return strings.ReplaceAll(err.Error(), "\n", " ")
}
