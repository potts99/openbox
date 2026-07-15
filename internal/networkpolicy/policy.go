// SPDX-License-Identifier: AGPL-3.0-only

// Package networkpolicy evaluates instance egress policy decisions.
package networkpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"

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

const (
	restrictedACLPrefix     = "obx-egr-"
	restrictedACLHashLength = 12
	incusACLNameMaxLength   = 63
)

// RestrictedACLName returns the deterministic, Incus-safe ACL name for an
// instance's restricted egress policy. InstanceID, rather than guest IP, is
// the canonical policy identity because guest IPs can change across restarts.
func RestrictedACLName(instanceID string) string {
	sanitizedID := sanitizeACLNameComponent(instanceID)
	hash := sha256.Sum256([]byte(instanceID))
	hashSuffix := hex.EncodeToString(hash[:])[:restrictedACLHashLength]
	maxIDLength := incusACLNameMaxLength - len(restrictedACLPrefix) - len(hashSuffix) - 1
	if len(sanitizedID) > maxIDLength {
		sanitizedID = sanitizedID[:maxIDLength]
	}
	return restrictedACLPrefix + sanitizedID + "-" + hashSuffix
}

func sanitizeACLNameComponent(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('-')
		}
	}
	sanitized := strings.Trim(builder.String(), "-")
	if sanitized == "" {
		return "instance"
	}
	return sanitized
}

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

// ParseAllowlistHostnames parses exact administrator allowlist hostnames.
// IP addresses and CIDRs remain the responsibility of ParseAllowedDestinations.
func ParseAllowlistHostnames(raw []byte) ([]string, error) {
	var entries []string
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse allowlist hostnames: %w", err)
	}
	if entries == nil {
		return nil, fmt.Errorf("allowlist hostnames must be a JSON array")
	}

	hostnames := make([]string, 0, len(entries))
	for _, entry := range entries {
		hostname := strings.ToLower(strings.TrimSuffix(entry, "."))
		if !isExactHostname(hostname) {
			return nil, fmt.Errorf("allowlist hostname %q must be an exact hostname", entry)
		}
		hostnames = append(hostnames, hostname)
	}
	return hostnames, nil
}

func isExactHostname(hostname string) bool {
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
