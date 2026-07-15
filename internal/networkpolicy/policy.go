// SPDX-License-Identifier: AGPL-3.0-only

// Package networkpolicy evaluates instance egress policy decisions.
package networkpolicy

import "github.com/openbox-dev/openbox/internal/domain"

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
