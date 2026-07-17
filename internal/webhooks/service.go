// SPDX-License-Identifier: AGPL-3.0-only

// Package webhooks manages owner-scoped outbound webhook subscriptions.
package webhooks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

const (
	MaxSubscriptions = 10
	MaxURLLength     = 2048
	MaxDescription   = 200
)

var eventTypes = map[string]struct{}{
	"operation.terminal": {},
	"instance.expired":   {},
	"instance.deleted":   {},
}

type Repository interface {
	CreateWebhookSubscription(context.Context, domain.WebhookSubscription) error
	ListWebhookSubscriptions(context.Context, domain.OwnerID) ([]domain.WebhookSubscription, error)
	GetWebhookSubscription(context.Context, domain.OwnerID, domain.WebhookSubscriptionID) (domain.WebhookSubscription, error)
	UpdateWebhookSubscription(context.Context, domain.OwnerID, domain.WebhookSubscriptionID, *string, *string, []string, bool, *bool, *string, time.Time) (domain.WebhookSubscription, error)
	DeleteWebhookSubscription(context.Context, domain.OwnerID, domain.WebhookSubscriptionID, time.Time) error
}

type Service struct {
	repo  Repository
	now   func() time.Time
	newID func(string) (string, error)
}

type CreateInput struct {
	OwnerID     domain.OwnerID
	URL         string
	Description string
	Events      []string
	Enabled     *bool
}

type UpdateInput struct {
	URL          *string
	Description  *string
	Events       *[]string
	Enabled      *bool
	RotateSecret bool
}

func New(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, errors.New("webhook repository is required")
	}
	return &Service{repo: repo, now: time.Now, newID: randomID}, nil
}

func (s *Service) Create(ctx context.Context, input CreateInput) (domain.WebhookSubscription, error) {
	if err := validate(input.URL, input.Description, input.Events); err != nil {
		return domain.WebhookSubscription{}, err
	}
	id, err := s.newID("whsub_")
	if err != nil {
		return domain.WebhookSubscription{}, err
	}
	secret, err := s.newID("whsec_")
	if err != nil {
		return domain.WebhookSubscription{}, err
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	now := s.now().UTC()
	subscription := domain.WebhookSubscription{
		ID:          domain.WebhookSubscriptionID(id),
		OwnerID:     input.OwnerID,
		URL:         strings.TrimSpace(input.URL),
		Description: strings.TrimSpace(input.Description),
		Secret:      secret,
		Events:      append([]string(nil), input.Events...),
		Enabled:     enabled,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.CreateWebhookSubscription(ctx, subscription); err != nil {
		return domain.WebhookSubscription{}, err
	}
	return subscription, nil
}

func (s *Service) List(ctx context.Context, ownerID domain.OwnerID) ([]domain.WebhookSubscription, error) {
	return s.repo.ListWebhookSubscriptions(ctx, ownerID)
}

func (s *Service) Get(ctx context.Context, ownerID domain.OwnerID, id domain.WebhookSubscriptionID) (domain.WebhookSubscription, error) {
	return s.repo.GetWebhookSubscription(ctx, ownerID, id)
}

func (s *Service) Update(ctx context.Context, ownerID domain.OwnerID, id domain.WebhookSubscriptionID, input UpdateInput) (domain.WebhookSubscription, error) {
	current, err := s.repo.GetWebhookSubscription(ctx, ownerID, id)
	if err != nil {
		return domain.WebhookSubscription{}, err
	}
	url := current.URL
	if input.URL != nil {
		url = *input.URL
	}
	description := current.Description
	if input.Description != nil {
		description = *input.Description
	}
	events := current.Events
	if input.Events != nil {
		events = *input.Events
	}
	if err := validate(url, description, events); err != nil {
		return domain.WebhookSubscription{}, err
	}
	var secret *string
	if input.RotateSecret {
		value, err := s.newID("whsec_")
		if err != nil {
			return domain.WebhookSubscription{}, err
		}
		secret = &value
	}
	return s.repo.UpdateWebhookSubscription(ctx, ownerID, id, input.URL, input.Description, events, input.Events != nil, input.Enabled, secret, s.now().UTC())
}

func (s *Service) Delete(ctx context.Context, ownerID domain.OwnerID, id domain.WebhookSubscriptionID) error {
	return s.repo.DeleteWebhookSubscription(ctx, ownerID, id, s.now().UTC())
}

func validate(rawURL, description string, events []string) error {
	rawURL = strings.TrimSpace(rawURL)
	if len(rawURL) == 0 || len(rawURL) > MaxURLLength {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "url"}
	}
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "url"}
	}
	if len(description) > MaxDescription {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "description"}
	}
	if len(events) == 0 {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "events"}
	}
	seen := map[string]struct{}{}
	for _, event := range events {
		if _, ok := eventTypes[event]; !ok {
			return &domain.Error{Code: domain.CodeInvalidArgument, Field: "events"}
		}
		if _, duplicate := seen[event]; duplicate {
			return &domain.Error{Code: domain.CodeInvalidArgument, Field: "events"}
		}
		seen[event] = struct{}{}
	}
	return nil
}

func randomID(prefix string) (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value[:]), nil
}
