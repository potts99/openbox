// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"

	"github.com/openbox-dev/openbox/internal/domain"
)

const routeTokenPrefix = "obr_"

// AccessCredentials are credentials presented at the HTTPS gateway for a Host.
// OwnerID is set when an owner session or owner API bearer already authenticated.
// RouteToken is a route-scoped secret (obr_…).
type AccessCredentials struct {
	OwnerID    domain.OwnerID
	RouteToken string
}

// RouteToken is a scoped credential for one private route. Secret is returned
// only on create; listings omit it.
type RouteToken struct {
	ID        string
	RouteID   domain.RouteID
	OwnerID   domain.OwnerID
	Name      string
	CreatedAt time.Time
	Secret    string // create response only
}

// RouteTokenRepository persists route-scoped tokens.
type RouteTokenRepository interface {
	CreateRouteToken(context.Context, RouteToken, []byte) error
	ListRouteTokens(context.Context, domain.OwnerID, domain.RouteID) ([]RouteToken, error)
	RevokeRouteToken(context.Context, domain.OwnerID, domain.RouteID, string, time.Time) error
	FindRouteToken(context.Context, []byte, time.Time) (RouteToken, error)
}

// AuthorizeAccess allows gateway traffic for hostname.
// Public routes bypass credentials. Private routes require the matching owner
// principal or a non-revoked route-scoped token for that route.
func (s *Service) AuthorizeAccess(ctx context.Context, hostname string, creds AccessCredentials) error {
	normalized, ok := normalizeCertificateHostname(hostname)
	if !ok {
		return &domain.Error{Code: domain.CodeNotFound, Field: "hostname"}
	}
	route, found, err := s.repo.FindRouteByHostname(ctx, normalized)
	if err != nil {
		return err
	}
	if !found {
		return &domain.Error{Code: domain.CodeNotFound, Field: "hostname"}
	}
	if route.Visibility == domain.RoutePublic {
		return nil
	}
	if route.Visibility != domain.RoutePrivate {
		return &domain.Error{Code: domain.CodeInvalidArgument, Field: "visibility"}
	}
	if creds.OwnerID != "" {
		if creds.OwnerID != route.OwnerID {
			return &domain.Error{Code: domain.CodeForbidden, Field: "authorization"}
		}
		return nil
	}
	if secret := strings.TrimSpace(creds.RouteToken); secret != "" {
		return s.authorizeRouteToken(ctx, route, secret)
	}
	return &domain.Error{Code: domain.CodeUnauthenticated, Field: "authorization"}
}

func (s *Service) authorizeRouteToken(ctx context.Context, route domain.Route, secret string) error {
	tokens, ok := s.repo.(RouteTokenRepository)
	if !ok {
		return &domain.Error{Code: domain.CodeNotImplemented, Field: "route_tokens"}
	}
	if !strings.HasPrefix(secret, routeTokenPrefix) {
		return &domain.Error{Code: domain.CodeUnauthenticated, Field: "authorization"}
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(secret, routeTokenPrefix))
	if err != nil || len(raw) == 0 {
		return &domain.Error{Code: domain.CodeUnauthenticated, Field: "authorization"}
	}
	sum := sha256.Sum256(raw)
	token, err := tokens.FindRouteToken(ctx, sum[:], s.now().UTC())
	if err != nil {
		return &domain.Error{Code: domain.CodeUnauthenticated, Field: "authorization"}
	}
	if token.RouteID != route.ID || token.OwnerID != route.OwnerID {
		return &domain.Error{Code: domain.CodeForbidden, Field: "authorization"}
	}
	return nil
}

// CreateRouteToken issues a route-scoped secret for a private or public route
// (tokens are useful for private access; allowed on any owned route).
func (s *Service) CreateRouteToken(ctx context.Context, ownerID domain.OwnerID, routeID domain.RouteID, name string) (RouteToken, error) {
	tokens, ok := s.repo.(RouteTokenRepository)
	if !ok {
		return RouteToken{}, &domain.Error{Code: domain.CodeNotImplemented, Field: "route_tokens"}
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return RouteToken{}, &domain.Error{Code: domain.CodeInvalidArgument, Field: "name"}
	}
	route, err := s.repo.GetRoute(ctx, ownerID, routeID)
	if err != nil {
		return RouteToken{}, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return RouteToken{}, err
	}
	idRaw := make([]byte, 12)
	if _, err := rand.Read(idRaw); err != nil {
		return RouteToken{}, err
	}
	sum := sha256.Sum256(raw)
	token := RouteToken{
		ID:        "rtok_" + hex.EncodeToString(idRaw),
		RouteID:   route.ID,
		OwnerID:   ownerID,
		Name:      name,
		CreatedAt: s.now().UTC(),
		Secret:    routeTokenPrefix + base64.RawURLEncoding.EncodeToString(raw),
	}
	if err := tokens.CreateRouteToken(ctx, token, sum[:]); err != nil {
		return RouteToken{}, err
	}
	return token, nil
}

// ListRouteTokens returns metadata without secrets.
func (s *Service) ListRouteTokens(ctx context.Context, ownerID domain.OwnerID, routeID domain.RouteID) ([]RouteToken, error) {
	tokens, ok := s.repo.(RouteTokenRepository)
	if !ok {
		return nil, &domain.Error{Code: domain.CodeNotImplemented, Field: "route_tokens"}
	}
	if _, err := s.repo.GetRoute(ctx, ownerID, routeID); err != nil {
		return nil, err
	}
	return tokens.ListRouteTokens(ctx, ownerID, routeID)
}

// RevokeRouteToken invalidates a route-scoped token.
func (s *Service) RevokeRouteToken(ctx context.Context, ownerID domain.OwnerID, routeID domain.RouteID, tokenID string) error {
	tokens, ok := s.repo.(RouteTokenRepository)
	if !ok {
		return &domain.Error{Code: domain.CodeNotImplemented, Field: "route_tokens"}
	}
	if _, err := s.repo.GetRoute(ctx, ownerID, routeID); err != nil {
		return err
	}
	return tokens.RevokeRouteToken(ctx, ownerID, routeID, tokenID, s.now().UTC())
}
