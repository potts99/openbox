// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/doctor"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

type commandDiscoverer struct{}

func (commandDiscoverer) DiscoverCapabilities(context.Context) (runtimeapi.Capabilities, error) {
	return runtimeapi.Capabilities{
		Architecture: "x86_64", IncusVersion: "test", Containers: true,
		Namespaces: map[string]bool{"mnt": true, "net": true, "pid": true, "user": true},
		Cgroups:    true, StorageDrivers: []string{"dir"},
		NetworkTools: map[string]bool{"dnsmasq": true, "ip": true, "nft": true},
	}, nil
}

func TestDoctorJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	factory := func(socket string, timeout time.Duration) (doctor.Discoverer, error) {
		if socket != "/test/incus.socket" || timeout != 2*time.Second {
			t.Fatalf("factory arguments = %q, %s", socket, timeout)
		}
		return commandDiscoverer{}, nil
	}
	code := run([]string{"doctor", "--json", "--socket", "/test/incus.socket", "--timeout", "2s"}, &stdout, &stderr, factory)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	var report doctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if len(report.Checks) == 0 || report.Capabilities.IncusVersion != "test" {
		t.Fatalf("report = %#v", report)
	}
}

func TestDoctorHumanOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor"}, &stdout, &stderr, func(string, time.Duration) (doctor.Discoverer, error) {
		return commandDiscoverer{}, nil
	})
	if code != 0 || !bytes.Contains(stdout.Bytes(), []byte("strong-isolation")) {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout.String(), stderr.String())
	}
}
