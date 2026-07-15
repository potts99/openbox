// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"context"
	"net"

	"github.com/openbox-dev/openbox/internal/domain"
)

// DNS / TLS activation states stored on domain.Route.TLSState.
const (
	TLSStatePending = "pending"
	TLSStateInvalid = "invalid"
	TLSStateActive  = "active"
)

// DNSResolver looks up A/AAAA records for custom-domain validation.
type DNSResolver interface {
	LookupIP(ctx context.Context, host string) ([]net.IP, error)
}

// ValidateDNS checks whether the route hostname resolves to an expected host
// address and persists an actionable TLSState: pending, invalid, or active.
func (s *Service) ValidateDNS(ctx context.Context, ownerID domain.OwnerID, id domain.RouteID) (domain.Route, error) {
	route, err := s.repo.GetRoute(ctx, ownerID, id)
	if err != nil {
		return domain.Route{}, err
	}
	state := TLSStatePending
	if len(s.expectedIPs) == 0 || s.dns == nil {
		state = TLSStatePending
	} else {
		ips, lookupErr := s.dns.LookupIP(ctx, route.Hostname)
		switch {
		case lookupErr != nil || len(ips) == 0:
			state = TLSStatePending
		case ipsMatchExpected(ips, s.expectedIPs):
			state = TLSStateActive
		default:
			state = TLSStateInvalid
		}
	}
	route.TLSState = state
	route.UpdatedAt = s.now().UTC()
	if err := s.repo.UpdateRoute(ctx, route); err != nil {
		return domain.Route{}, err
	}
	return route, nil
}

func ipsMatchExpected(got, expected []net.IP) bool {
	for _, want := range expected {
		want4 := want.To4()
		for _, have := range got {
			have4 := have.To4()
			if want4 != nil && have4 != nil && want4.Equal(have4) {
				return true
			}
			if want4 == nil && have4 == nil && want.Equal(have) {
				return true
			}
		}
	}
	return false
}
