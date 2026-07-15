// SPDX-License-Identifier: AGPL-3.0-only

// Package dnsproxy resolves allowlisted hostnames on the host and filters
// results that could bypass guest egress policy through DNS rebinding.
package dnsproxy

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/openbox-dev/openbox/internal/clock"
)

var (
	cgnatPrefix  = netip.MustParsePrefix("100.64.0.0/10")
	bridgePrefix = netip.MustParsePrefix("10.42.0.0/24")
)

// LookupResult contains addresses returned for a hostname and their DNS TTL.
type LookupResult struct {
	Addresses []netip.Addr
	TTL       time.Duration
}

// Resolver performs host-side DNS resolution.
type Resolver interface {
	Lookup(context.Context, string) (LookupResult, error)
}

// NetResolver resolves hostnames through the host resolver. The Go standard
// library does not expose DNS TTLs, so callers use Config.MinTTL for its
// refresh interval.
type NetResolver struct {
	Resolver *net.Resolver
}

func (r NetResolver) Lookup(ctx context.Context, hostname string) (LookupResult, error) {
	resolver := r.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, err := resolver.LookupNetIP(ctx, "ip", hostname)
	if err != nil {
		return LookupResult{}, err
	}
	addresses := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		addresses = append(addresses, ip.Unmap())
	}
	return LookupResult{Addresses: addresses}, nil
}

type Config struct {
	Resolver Resolver
	Clock    clock.Clock
	MinTTL   time.Duration
	MaxTTL   time.Duration
}

type cacheEntry struct {
	addresses []netip.Addr
	expiresAt time.Time
}

// AllowlistResolver resolves and caches DNS allowlist entries safely.
type AllowlistResolver struct {
	resolver Resolver
	clock    clock.Clock
	minTTL   time.Duration
	maxTTL   time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
}

func NewAllowlistResolver(config Config) (*AllowlistResolver, error) {
	if config.Resolver == nil {
		config.Resolver = NetResolver{}
	}
	if config.Clock == nil {
		config.Clock = clock.Real{}
	}
	if config.MinTTL <= 0 {
		config.MinTTL = time.Minute
	}
	if config.MaxTTL <= 0 {
		config.MaxTTL = 5 * time.Minute
	}
	if config.MaxTTL < config.MinTTL {
		return nil, fmt.Errorf("dns allowlist max TTL must be at least min TTL")
	}
	return &AllowlistResolver{
		resolver: config.Resolver,
		clock:    config.Clock,
		minTTL:   config.MinTTL,
		maxTTL:   config.MaxTTL,
		cache:    make(map[string]cacheEntry),
	}, nil
}

// Resolve returns the current safe addresses for an allowlisted hostname. An
// expired cached entry is never returned when its refresh fails.
func (r *AllowlistResolver) Resolve(ctx context.Context, hostname string) ([]netip.Addr, error) {
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	now := r.clock.Now()
	r.mu.Lock()
	entry, ok := r.cache[hostname]
	r.mu.Unlock()
	if ok && now.Before(entry.expiresAt) {
		return append([]netip.Addr(nil), entry.addresses...), nil
	}

	result, err := r.resolver.Lookup(ctx, hostname)
	if err != nil {
		return nil, fmt.Errorf("resolve allowlist hostname %q: %w", hostname, err)
	}
	addresses := safeAddresses(result.Addresses)
	if len(addresses) == 0 {
		return nil, fmt.Errorf("resolve allowlist hostname %q: no public addresses", hostname)
	}

	ttl := result.TTL
	if ttl < r.minTTL {
		ttl = r.minTTL
	}
	if ttl > r.maxTTL {
		ttl = r.maxTTL
	}
	r.mu.Lock()
	r.cache[hostname] = cacheEntry{
		addresses: append([]netip.Addr(nil), addresses...),
		expiresAt: now.Add(ttl),
	}
	r.mu.Unlock()
	return addresses, nil
}

func safeAddresses(addresses []netip.Addr) []netip.Addr {
	safe := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if address.IsValid() && !isRebindingAddress(address) {
			safe = append(safe, address)
		}
	}
	return safe
}

func isRebindingAddress(address netip.Addr) bool {
	if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() ||
		address.IsMulticast() || address.IsUnspecified() {
		return true
	}
	if address.Is4() && (cgnatPrefix.Contains(address) || bridgePrefix.Contains(address) ||
		address == netip.MustParseAddr("255.255.255.255")) {
		return true
	}
	return false
}
