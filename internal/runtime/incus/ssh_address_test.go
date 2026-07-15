// SPDX-License-Identifier: AGPL-3.0-only

package incus

import (
	"net"
	"testing"
)

func TestSelectInstanceAddressPrefersPrivateAndRejectsUnsafeValues(t *testing.T) {
	state := instanceStateRecord{Network: map[string]instanceStateNetwork{}}
	network := state.Network["eth0"]
	for _, value := range []string{"", "not-an-ip", "0.0.0.0", "224.0.0.1", "169.254.1.2", "203.0.113.8", "10.42.0.12"} {
		network.Addresses = append(network.Addresses, instanceStateAddress{Family: "inet", Address: value, Scope: "global"})
	}
	state.Network["eth0"] = network
	if address, found := selectInstanceAddress(state, true); !found || address != "10.42.0.12" {
		t.Fatalf("private address=%q found=%v", address, found)
	}
	state.Network["eth0"] = instanceStateNetwork{Addresses: network.Addresses[:len(network.Addresses)-1]}
	if address, found := selectInstanceAddress(state, true); found || address != "" {
		t.Fatalf("public-only private address=%q found=%v", address, found)
	}
	if address, found := selectInstanceAddress(state, false); !found || address != "203.0.113.8" {
		t.Fatalf("readiness fallback=%q found=%v", address, found)
	}
	_, subnet, _ := net.ParseCIDR("10.42.0.0/24")
	state.Network["eth0"] = network
	if address, found := selectInstanceAddressInNetwork(state, subnet); !found || address != "10.42.0.12" {
		t.Fatalf("managed address=%q found=%v", address, found)
	}
	_, wrongSubnet, _ := net.ParseCIDR("10.43.0.0/24")
	if address, found := selectInstanceAddressInNetwork(state, wrongSubnet); found || address != "" {
		t.Fatalf("out-of-network address=%q found=%v", address, found)
	}
}
