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

var (
	allowlistCGNATPrefix  = netip.MustParsePrefix("100.64.0.0/10")
	allowlistBridgePrefix = netip.MustParsePrefix("10.42.0.0/24")
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
	literals, _, err := ParseAllowlistEntries(p.AllowedDestinationsJSON)
	if err != nil {
		return newError(CodeInvalidArgument, "allowed_destinations")
	}
	if p.Mode == EgressRestricted {
		for _, literal := range literals {
			if !SafeRestrictedAllowlistLiteral(literal) {
				return newError(CodeInvalidArgument, "allowed_destinations")
			}
		}
	}
	return nil
}

// SafeRestrictedAllowlistLiteral reports whether an IP/CIDR may appear on a
// restricted allowlist. Public internet and private/bridge bypasses are rejected.
func SafeRestrictedAllowlistLiteral(literal string) bool {
	if address, err := netip.ParseAddr(literal); err == nil {
		return !unsafeAllowlistAddr(address)
	}
	prefix, err := netip.ParsePrefix(literal)
	if err != nil {
		return false
	}
	if prefix.Bits() == 0 {
		return false
	}
	if unsafeAllowlistAddr(prefix.Addr()) {
		return false
	}
	for _, blocked := range []netip.Prefix{allowlistCGNATPrefix, allowlistBridgePrefix} {
		if prefix.Overlaps(blocked) {
			return false
		}
	}
	// Reject any prefix that overlaps RFC1918 / loopback / link-local by
	// testing containment of common private networks.
	for _, privateNet := range []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"127.0.0.0/8", "169.254.0.0/16", "::1/128", "fc00::/7", "fe80::/10",
	} {
		if prefix.Overlaps(netip.MustParsePrefix(privateNet)) {
			return false
		}
	}
	return true
}

func unsafeAllowlistAddr(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || address.IsLoopback() || address.IsPrivate() ||
		address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return true
	}
	if address.Is4() && (allowlistCGNATPrefix.Contains(address) || allowlistBridgePrefix.Contains(address) ||
		address == netip.MustParseAddr("255.255.255.255")) {
		return true
	}
	return false
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
