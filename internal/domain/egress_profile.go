// SPDX-License-Identifier: AGPL-3.0-only

package domain

import (
	"encoding/json"
	"net/netip"
	"strings"
)

const (
	EgressProfileNameStandard   = "standard"
	EgressProfileNameRestricted = "restricted"

	EgressProfileIDStandard   EgressProfileID = "egress-standard"
	EgressProfileIDRestricted EgressProfileID = "egress-restricted"

	DNSPolicyHostResolve = "host_resolve"
)

// DefaultEgressProfileID returns the seeded system profile for a kind.
func DefaultEgressProfileID(kind InstanceKind) EgressProfileID {
	if kind == KindSandbox {
		return EgressProfileIDRestricted
	}
	return EgressProfileIDStandard
}

// Validate checks profile identity, mode, DNS policy, and allowlist entries.
func (p EgressProfile) Validate() error {
	if p.ID == "" {
		return newError(CodeInvalidArgument, "id")
	}
	if err := ValidateInstanceName(p.Name); err != nil {
		return newError(CodeInvalidArgument, "name")
	}
	switch p.Mode {
	case EgressStandard, EgressRestricted:
	default:
		return newError(CodeInvalidArgument, "mode")
	}
	if p.DNSPolicy != DNSPolicyHostResolve {
		return newError(CodeInvalidArgument, "dns_policy")
	}
	if _, _, err := ParseAllowlistEntries(p.AllowedDestinationsJSON); err != nil {
		return newError(CodeInvalidArgument, "allowed_destinations")
	}
	return nil
}

// ParseAllowlistEntries splits a JSON array into IP/CIDR literals and exact hostnames.
func ParseAllowlistEntries(raw []byte) (literals []string, hostnames []string, err error) {
	if raw == nil {
		return nil, nil, newError(CodeInvalidArgument, "allowed_destinations")
	}
	var entries []string
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, nil, newError(CodeInvalidArgument, "allowed_destinations")
	}
	if entries == nil {
		return nil, nil, newError(CodeInvalidArgument, "allowed_destinations")
	}
	literals = make([]string, 0, len(entries))
	hostnames = make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, nil, newError(CodeInvalidArgument, "allowed_destinations")
		}
		if address, err := netip.ParseAddr(entry); err == nil {
			literals = append(literals, address.String())
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			literals = append(literals, prefix.Masked().String())
			continue
		}
		hostname := strings.ToLower(strings.TrimSuffix(entry, "."))
		if !isExactAllowlistHostname(hostname) {
			return nil, nil, newError(CodeInvalidArgument, "allowed_destinations")
		}
		hostnames = append(hostnames, hostname)
	}
	return literals, hostnames, nil
}

func isExactAllowlistHostname(hostname string) bool {
	if hostname == "" {
		return false
	}
	if _, err := netip.ParseAddr(hostname); err == nil {
		return false
	}
	for _, label := range strings.Split(hostname, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if char != '-' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
				return false
			}
		}
	}
	return true
}
