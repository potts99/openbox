// SPDX-License-Identifier: AGPL-3.0-only

package acceptance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openbox-dev/openbox/internal/app/egress"
	"github.com/openbox-dev/openbox/internal/app/instances"
	"github.com/openbox-dev/openbox/internal/clock"
	"github.com/openbox-dev/openbox/internal/domain"
	"github.com/openbox-dev/openbox/internal/httpapi"
	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/runtime/fake"
)

// TestAgentSafetyEgressParity covers the Phase 3 no-live-host gate:
// default restricted sandbox profile, attach/update apply result, and durable
// policy audit events without payloads.
func TestAgentSafetyEgressParity(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 7, 16, 15, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(start)
	runtime := fake.New(runtimeapi.Capabilities{Architecture: "x86_64", Containers: true})
	runtime.AddImage(sandboxImage())
	store := openStore(t)
	if err := store.EnsureSystemEgressProfiles(ctx); err != nil {
		t.Fatal(err)
	}

	policyRuntime := &recordingPolicyRuntime{}
	applicator := egress.NewApplicator(nil, policyRuntime)
	egressService, err := egress.New(store, applicator, egress.Options{
		Now: fakeClock.Now,
		NewID: func() string {
			return "egress-custom"
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	auditor := &egress.DurablePolicyAuditor{
		Store: store,
		Now:   fakeClock.Now,
		NewID: sequentialIDs(),
	}
	egressService.SetAuditor(auditor)

	bridge := &egress.PolicyBridge{Profiles: store, Applicator: applicator, Backend: &statusBackend{runtime: policyRuntime}}
	service, err := instances.New(runtime, store, instances.Options{
		Now: fakeClock.Now, NewID: sequentialIDs(), NetworkPolicy: bridge,
	})
	if err != nil {
		t.Fatal(err)
	}
	egressService.SetInstanceMarker(service)

	created, err := service.Create(ctx, instances.CreateInput{
		OwnerID: "owner-1", Name: "agent-safe", Kind: domain.KindSandbox,
		OwnerPublicKey: "ssh-ed25519 owner", IdempotencyKey: "accept-safety-create",
		Lifetime: 1 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.EgressProfileID != domain.EgressProfileIDRestricted {
		t.Fatalf("egress_profile_id=%q", created.EgressProfileID)
	}
	if created.EgressMode != domain.EgressRestricted {
		t.Fatalf("egress_mode=%q", created.EgressMode)
	}
	if created.NetworkPolicy.EgressMode != domain.EgressRestricted {
		t.Fatalf("network_policy=%#v", created.NetworkPolicy)
	}

	custom, err := egressService.Create(ctx, egress.CreateProfileInput{
		Name: "allow-one", Mode: domain.EgressRestricted,
		AllowedDestinations: []string{"203.0.113.10"},
	})
	if err != nil {
		t.Fatal(err)
	}
	attached, err := service.AttachEgressProfile(ctx, created.OwnerID, created.ID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if attached.EgressProfileID != custom.ID {
		t.Fatalf("attached profile=%q", attached.EgressProfileID)
	}
	if len(policyRuntime.applied) == 0 {
		t.Fatal("expected policy apply during attach")
	}

	destinations := []string{"203.0.113.10", "198.51.100.20"}
	updated, applyErrors, err := egressService.Update(ctx, custom.ID, egress.UpdateProfileInput{
		AllowedDestinations: &destinations,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(applyErrors) != 0 {
		t.Fatalf("apply_errors=%#v", applyErrors)
	}
	var got []string
	if err := json.Unmarshal(updated.AllowedDestinationsJSON, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("destinations=%v", got)
	}

	events, err := store.ListAuditEvents(ctx, "owner-1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected policy audit events")
	}
	foundApply := false
	for _, event := range events {
		if event.Action == egress.ActionPolicyApply {
			foundApply = true
		}
		var meta map[string]string
		if err := json.Unmarshal(event.MetadataJSON, &meta); err != nil {
			t.Fatal(err)
		}
		for key, value := range meta {
			if key == "message" && looksLikeAddress(value) {
				t.Fatalf("audit metadata leaked address-like value: %s=%q", key, value)
			}
		}
	}
	if !foundApply {
		t.Fatalf("actions=%v", eventActions(events))
	}

	if _, err := egressService.Create(ctx, egress.CreateProfileInput{
		Name: "bypass", Mode: domain.EgressRestricted,
		AllowedDestinations: []string{"0.0.0.0/0"},
	}); err == nil {
		t.Fatal("restricted profile must reject 0.0.0.0/0")
	}

	hosts, err := store.CreateEgressProfile(ctx, domain.EgressProfile{
		ID: "egress-hosts", Name: "hosts", Mode: domain.EgressRestricted,
		AllowedDestinationsJSON: []byte(`["example.com"]`), DNSPolicy: domain.DNSPolicyHostResolve,
		CreatedAt: start, UpdatedAt: start,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateInstanceEgressProfile(ctx, created.OwnerID, created.ID, hosts.ID, domain.EgressRestricted, start); err != nil {
		t.Fatal(err)
	}
	beforeRefresh, err := service.GetInstance(ctx, created.OwnerID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	report, err := egressService.RefreshHostnameAllowlists(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Errors) == 0 {
		t.Fatal("expected refresh failure without DNS resolver")
	}
	afterRefresh, err := service.GetInstance(ctx, created.OwnerID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterRefresh.ObservedState != beforeRefresh.ObservedState {
		t.Fatalf("refresh must not change observed_state: before=%q after=%q", beforeRefresh.ObservedState, afterRefresh.ObservedState)
	}

	handler, err := httpapi.New(service, httpapi.Options{
		OwnerID: "owner-1", EgressProfiles: egressService, AuditEvents: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/audit-events?limit=20", nil)
	request.Header.Set(httpapi.HeaderAPIVersion, httpapi.APIVersion)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) == 0 {
		t.Fatal("expected audit events from HTTP API")
	}
	foundRefreshFailed := false
	for _, item := range body.Items {
		if item["action"] == egress.ActionPolicyRefreshFailed {
			foundRefreshFailed = true
		}
	}
	if !foundRefreshFailed {
		t.Fatalf("expected policy.refresh_failed in %#v", body.Items)
	}
}

type recordingPolicyRuntime struct {
	applied []domain.InstanceID
}

func (r *recordingPolicyRuntime) ProgramNetworkPolicy(_ context.Context, apply egress.PolicyApply) error {
	r.applied = append(r.applied, apply.Instance.ID)
	return nil
}

func (r *recordingPolicyRuntime) SetAllowlistResolution(domain.InstanceID, domain.AllowlistResolution) {}

type statusBackend struct {
	runtime *recordingPolicyRuntime
}

func (b *statusBackend) RemoveNetworkPolicy(context.Context, domain.Instance) error { return nil }
func (b *statusBackend) VerifyNetworkPolicy(context.Context, domain.Instance) error { return nil }
func (b *statusBackend) NetworkPolicyStatus(instance domain.Instance) domain.NetworkPolicyStatus {
	return domain.NetworkPolicyStatus{
		EgressMode: instance.EgressMode,
		ACLs:       []string{"openbox-default-deny"},
		Resolution: domain.AllowlistResolution{State: "idle", Pending: []string{}, Resolved: []string{}, Failed: []string{}},
	}
}

func eventActions(events []domain.AuditEvent) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.Action)
	}
	return out
}

func looksLikeAddress(value string) bool {
	for _, prefix := range []string{"203.0.113.", "198.51.100.", "10.42.0."} {
		if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
