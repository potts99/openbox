// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"context"
	"strings"
	"unicode"
)

// maxCertificateHostnameLen is the DNS wire-form limit for a full name.
const maxCertificateHostnameLen = 253

// CertificateAllowed reports whether on-demand TLS may issue a certificate for
// hostname. Approved means an existing persisted route hostname (any
// visibility). Unknown, deleted, and malformed names are denied. The check is
// case-insensitive.
func (s *Service) CertificateAllowed(ctx context.Context, hostname string) (bool, error) {
	normalized, ok := normalizeCertificateHostname(hostname)
	if !ok {
		return false, nil
	}
	_, found, err := s.repo.FindRouteByHostname(ctx, normalized)
	if err != nil {
		return false, err
	}
	return found, nil
}

// normalizeCertificateHostname trims, lowercases, and rejects names that are
// not safe hostname identifiers (path/port/URL/injection shapes).
func normalizeCertificateHostname(hostname string) (string, bool) {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" || len(hostname) > maxCertificateHostnameLen {
		return "", false
	}
	if strings.ContainsAny(hostname, "/\\:*?\"'`;\x00 ") || strings.Contains(hostname, "..") {
		return "", false
	}
	lower := strings.ToLower(hostname)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return "", false
	}
	labels := strings.Split(lower, ".")
	if len(labels) < 1 {
		return "", false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return "", false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, r := range label {
			if r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-') {
				return "", false
			}
		}
	}
	return lower, true
}
