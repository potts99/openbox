// SPDX-License-Identifier: AGPL-3.0-only

// Package networkpolicy evaluates instance egress policy decisions.
package networkpolicy

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/openbox-dev/openbox/internal/domain"
)

type DestinationClass string

const (
	DestinationHost         DestinationClass = "host"
	DestinationPeerInstance DestinationClass = "peer_instance"
	DestinationDNS          DestinationClass = "dns"
	DestinationInternet     DestinationClass = "internet"
	DestinationLLMGateway   DestinationClass = "llm_gateway"
	DestinationAllowlist    DestinationClass = "allowlist"
)

type Decision string

const (
	Allow Decision = "allow"
	Deny  Decision = "deny"
)

func Evaluate(mode domain.EgressMode, dest DestinationClass) Decision {
	switch mode {
	case domain.EgressStandard:
		switch dest {
		case DestinationDNS, DestinationInternet, DestinationLLMGateway, DestinationAllowlist:
			return Allow
		default:
			return Deny
		}
	case domain.EgressRestricted:
		switch dest {
		case DestinationDNS, DestinationLLMGateway, DestinationAllowlist:
			return Allow
		default:
			return Deny
		}
	default:
		return Deny
	}
}

// ParseAllowedDestinations parses administrator allowlist entries into their
// canonical IP address or masked CIDR forms. Hostname resolution is performed
// outside the guest in a later task.
func ParseAllowedDestinations(raw []byte) ([]string, error) {
	var entries []string
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse allowed destinations: %w", err)
	}
	if entries == nil {
		return nil, fmt.Errorf("allowed destinations must be a JSON array")
	}

	destinations := make([]string, 0, len(entries))
	for _, entry := range entries {
		if address, err := netip.ParseAddr(entry); err == nil {
			destinations = append(destinations, address.String())
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			destinations = append(destinations, prefix.Masked().String())
			continue
		}
		return nil, fmt.Errorf("allowed destination %q must be an IP address or CIDR", entry)
	}
	return destinations, nil
}
