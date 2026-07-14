// SPDX-License-Identifier: AGPL-3.0-only

package doctor_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openbox-dev/openbox/internal/doctor"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
)

type discoverer struct {
	capabilities runtimeapi.Capabilities
	err          error
}

func (d discoverer) DiscoverCapabilities(context.Context) (runtimeapi.Capabilities, error) {
	return d.capabilities, d.err
}

func TestContainerHostWithoutKVMIsUsable(t *testing.T) {
	report := doctor.Run(context.Background(), discoverer{capabilities: runtimeapi.Capabilities{
		Architecture: "x86_64", IncusVersion: "6.23", Containers: true,
		Namespaces: map[string]bool{"mnt": true, "net": true, "pid": true, "user": true},
		Cgroups:    true, StorageDrivers: []string{"dir"},
		NetworkTools: map[string]bool{"dnsmasq": true, "ip": true, "nft": false},
	}})
	if report.HasFatal() {
		t.Fatalf("container-capable host was fatal: %#v", report)
	}
	assertStatus(t, report, "standard-isolation", doctor.StatusPass)
	assertStatus(t, report, "strong-isolation", doctor.StatusUnavailable)
	assertStatus(t, report, "network-tooling", doctor.StatusWarning)
	human := doctor.FormatHuman(report)
	for _, wanted := range []string{"PASS", "WARNING", "UNAVAILABLE", "container mode remains supported"} {
		if !strings.Contains(human, wanted) {
			t.Fatalf("human output missing %q:\n%s", wanted, human)
		}
	}
}

func TestDaemonFailureIsFatalAndActionable(t *testing.T) {
	report := doctor.Run(context.Background(), discoverer{err: errors.New("permission denied")})
	if !report.HasFatal() || !strings.Contains(report.Checks[0].Guidance, "permissions") {
		t.Fatalf("report = %#v", report)
	}
}

func assertStatus(t *testing.T, report doctor.Report, name string, status doctor.Status) {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			if check.Status != status {
				t.Fatalf("%s status = %s, want %s", name, check.Status, status)
			}
			return
		}
	}
	t.Fatalf("missing check %s", name)
}
