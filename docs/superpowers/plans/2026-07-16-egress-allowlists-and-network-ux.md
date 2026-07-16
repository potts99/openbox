# Egress Allowlists and Network UX (Slice 19) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship system egress profiles with host-side DNS allowlist apply, immediate re-apply fan-out, and API/CLI/dashboard surfaces so restricted instances can reach only approved destinations.

**Architecture:** Host-global egress profiles own mode + allowlist. Instances reference a profile (`egress_profile_id`); create auto-attaches seeded `standard` or `restricted`. An apply orchestrator resolves hostnames via `dnsproxy`, programs Incus restricted ACLs, verifies NIC stacks, and tracks resolution state. Profile edits persist then fan-out re-apply without rolling back the profile row.

**Tech Stack:** Go, SQLite migrations, Incus network ACLs, OpenAPI (`oapi-codegen`), `openbox` CLI, React console (`web/`).

**Spec:** `docs/superpowers/specs/2026-07-16-egress-allowlists-and-network-ux-design.md` — read it before starting.

## Global Constraints

- Profiles are **system / host-global** (not per-owner); seeded `standard` and `restricted` are `system=true` and never deletable.
- Profile is **authoritative** for `egress_mode`; instance column is a denormalized cache.
- Allowlist entries: IP, CIDR, exact hostnames only (no wildcards); never expose resolved IPs in API/CLI/UI.
- Apply and TTL refresh are **fail-closed**; guests cannot relax policy.
- Profile edit fan-out: **keep** saved profile; mark failed instances error; return `apply_errors`.
- Delete profile rejected while any instance references it.
- No eBPF, TLS interception, cross-host policy, or Slice 15 LLM credentials.
- Empty restricted allowlist still valid (baseline DNS + LLM placeholder on `openbox-default-deny`).
- Prefer ensuring a named restricted ACL even when destinations are empty, stacked in `NICACLs`.
- Live Incus matrix is opt-in via `OPENBOX_INCUS_TEST_SOCKET` (skip otherwise).

## File structure

| Path | Responsibility |
|------|----------------|
| `internal/persistence/migrations/011_egress_profiles_system.sql` | System profiles schema + `instances.egress_profile_id` |
| `internal/domain/types.go`, `internal/domain/egress_profile.go` | Profile domain + validation |
| `internal/persistence/sqlite/egress_profiles.go` | Profile CRUD + seed + reference counts |
| `internal/persistence/sqlite/repositories.go` | Instance create/get with profile id |
| `internal/app/egress/` | Profile service + apply orchestration + fan-out |
| `internal/runtime/incus/acl.go` | EnsureRestrictedACL on apply; resolution state; NIC stack with restricted name |
| `internal/dnsproxy/` | Unchanged package; wired from `app/egress` |
| `internal/app/instances/service.go` | Default profile on create; attach; call applicator |
| `api/openapi.yaml` + `internal/httpapi/` | Profile CRUD + attach endpoints |
| `cmd/openbox/network.go` | CLI |
| `web/src/pages/NetworkPolicy.tsx` + Console/Instance wiring | Dashboard |
| `docs/security/networking.md`, `docs/operators/egress-profiles.md` | Docs |
| `docs/plans/19-egress-allowlists-and-network-ux.md` | Mark tasks complete when done |

---

### Task 1: Domain model and migration 011

**Files:**
- Create: `internal/persistence/migrations/011_egress_profiles_system.sql`
- Create: `internal/domain/egress_profile.go`
- Modify: `internal/domain/types.go` (EgressProfile, Instance)
- Modify: `internal/domain/instance.go` (validation)
- Test: `internal/domain/egress_profile_test.go`
- Test: `internal/persistence/sqlite/store_test.go` (migration count if asserted)

**Interfaces:**
- Consumes: existing `domain.EgressMode`, `domain.EgressProfileID`
- Produces:
  - `domain.EgressProfile` with `System bool`, no `OwnerID`
  - `domain.Instance.EgressProfileID EgressProfileID`
  - `domain.ParseAllowlistEntries([]string) (literals []string, hostnames []string, err error)` or validate helpers on profile
  - Constants: `EgressProfileNameStandard = "standard"`, `EgressProfileNameRestricted = "restricted"`
  - `DNSPolicyHostResolve = "host_resolve"`

- [ ] **Step 1: Write the failing domain tests**

```go
package domain_test

func TestEgressProfileValidate(t *testing.T) {
    p := domain.EgressProfile{
        ID: "prof-1", Name: "restricted", Mode: domain.EgressRestricted,
        AllowedDestinationsJSON: []byte(`["1.2.3.4","packages.example.com"]`),
        DNSPolicy: domain.DNSPolicyHostResolve,
    }
    if err := p.Validate(); err != nil {
        t.Fatal(err)
    }
}

func TestEgressProfileRejectsWildcardHostname(t *testing.T) {
    p := domain.EgressProfile{
        ID: "prof-1", Name: "custom", Mode: domain.EgressRestricted,
        AllowedDestinationsJSON: []byte(`["*.example.com"]`),
        DNSPolicy: domain.DNSPolicyHostResolve,
    }
    if err := p.Validate(); err == nil {
        t.Fatal("expected error")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/domain/ -run TestEgressProfile -count=1`  
Expected: FAIL (Validate undefined / type mismatch)

- [ ] **Step 3: Update domain types and validation**

In `types.go`, replace `EgressProfile` with:

```go
type EgressProfile struct {
    ID                      EgressProfileID
    Name                    string
    Mode                    EgressMode
    AllowedDestinationsJSON []byte
    DNSPolicy               string
    System                  bool
    CreatedAt, UpdatedAt    time.Time
}
```

Add `EgressProfileID` field on `Instance`. Implement `Validate()` in `egress_profile.go` using `networkpolicy.ParseAllowedDestinations` / `ParseAllowlistHostnames` on split entries (or a single JSON array that accepts mixed IP/CIDR/hostname — parse each entry as IP/CIDR else hostname).

- [ ] **Step 4: Add migration 011**

The `001` owner-scoped `egress_profiles` table has no Go readers/writers. Replace it outright:

```sql
-- SPDX-License-Identifier: AGPL-3.0-only
-- System-scoped egress profiles + instance binding.

DROP TABLE IF EXISTS egress_profiles;

CREATE TABLE egress_profiles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    mode TEXT NOT NULL CHECK(mode IN ('standard','restricted')),
    allowed_destinations_json BLOB NOT NULL,
    dns_policy TEXT NOT NULL,
    system INTEGER NOT NULL CHECK(system IN (0,1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT INTO egress_profiles (
    id, name, mode, allowed_destinations_json, dns_policy, system, created_at, updated_at
) VALUES
    ('egress-standard', 'standard', 'standard', '[]', 'host_resolve', 1, datetime('now'), datetime('now')),
    ('egress-restricted', 'restricted', 'restricted', '[]', 'host_resolve', 1, datetime('now'), datetime('now'));

ALTER TABLE instances ADD COLUMN egress_profile_id TEXT NOT NULL DEFAULT '';

UPDATE instances SET egress_profile_id = 'egress-restricted', egress_mode = 'restricted'
WHERE kind = 'sandbox';

UPDATE instances SET egress_profile_id = 'egress-standard', egress_mode = 'standard'
WHERE kind != 'sandbox';
```

- [ ] **Step 5: Run domain + sqlite migration tests**

Run: `go test ./internal/domain/ ./internal/persistence/sqlite/ -count=1`  
Expected: PASS (fix any migration count assertions — latest becomes `011_egress_profiles_system`)

- [ ] **Step 6: Commit**

```bash
git add internal/domain/ internal/persistence/migrations/011_egress_profiles_system.sql internal/persistence/sqlite/store_test.go
git commit -m "$(cat <<'EOF'
feat: add system egress profile domain and migration

EOF
)"
```

---

### Task 2: SQLite egress profile repository

**Files:**
- Create: `internal/persistence/sqlite/egress_profiles.go`
- Create: `internal/persistence/sqlite/egress_profiles_test.go`
- Modify: `internal/persistence/sqlite/repositories.go` (instance CRUD columns)

**Interfaces:**
- Consumes: `domain.EgressProfile`, migration 011 columns
- Produces:
  - `(*Store) EnsureSystemEgressProfiles(ctx) error`
  - `(*Store) CreateEgressProfile(ctx, domain.EgressProfile) (domain.EgressProfile, error)`
  - `(*Store) GetEgressProfile(ctx, domain.EgressProfileID) (domain.EgressProfile, error)`
  - `(*Store) GetEgressProfileByName(ctx, name string) (domain.EgressProfile, error)`
  - `(*Store) ListEgressProfiles(ctx) ([]domain.EgressProfile, error)`
  - `(*Store) UpdateEgressProfile(ctx, domain.EgressProfile) error`
  - `(*Store) DeleteEgressProfile(ctx, domain.EgressProfileID) error` — returns conflict if `system` or reference count > 0
  - `(*Store) CountInstancesWithEgressProfile(ctx, domain.EgressProfileID) (int, error)`
  - Instance create/get/list scan `egress_profile_id`

- [ ] **Step 1: Write failing repository tests**

```go
func TestEgressProfileCRUDAndDeleteGuards(t *testing.T) {
    store := openTestStore(t) // existing helper
    ctx := context.Background()
    if err := store.EnsureSystemEgressProfiles(ctx); err != nil {
        t.Fatal(err)
    }
    restricted, err := store.GetEgressProfileByName(ctx, "restricted")
    if err != nil || !restricted.System {
        t.Fatalf("seeded restricted: %#v %v", restricted, err)
    }
    if err := store.DeleteEgressProfile(ctx, restricted.ID); err == nil {
        t.Fatal("expected delete of system profile to fail")
    }
    custom := domain.EgressProfile{
        ID: domain.EgressProfileID("egress-custom"), Name: "allow-npm",
        Mode: domain.EgressRestricted,
        AllowedDestinationsJSON: []byte(`["registry.npmjs.org"]`),
        DNSPolicy: domain.DNSPolicyHostResolve,
    }
    created, err := store.CreateEgressProfile(ctx, custom)
    if err != nil {
        t.Fatal(err)
    }
    // create instance bound to custom, then DeleteEgressProfile must fail
    // delete instance binding, then delete succeeds
    _ = created
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/persistence/sqlite/ -run TestEgressProfileCRUD -count=1`  
Expected: FAIL

- [ ] **Step 3: Implement repository + instance column wiring**

Implement SQL in `egress_profiles.go`. Update `CreateInstance` INSERT and scanners in `repositories.go` to include `egress_profile_id`. When `EgressProfileID` empty on create, leave SQL default only if migration default exists — prefer service always sets it (Task 4).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/persistence/sqlite/ -count=1`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/persistence/sqlite/
git commit -m "$(cat <<'EOF'
feat: persist system egress profiles and instance binding

EOF
)"
```

---

### Task 3: Apply orchestration with dnsproxy + restricted ACL

**Files:**
- Create: `internal/app/egress/apply.go`
- Create: `internal/app/egress/apply_test.go`
- Modify: `internal/runtime/incus/acl.go` (`ApplyNetworkPolicy` signature / helpers)
- Modify: `internal/app/instances/service.go` (`NetworkPolicy` interface)
- Modify: fakes in `internal/app/instances/service_test.go`, `internal/runtime/incus/adapter_test.go`

**Interfaces:**
- Consumes: `dnsproxy.AllowlistResolver`, `networkpolicy.RestrictedACLName`, Incus `EnsureRestrictedACL`, `NICACLs`, `setInstanceNICACLs`, `VerifyNetworkPolicy`
- Produces:
  - `type ACLProgrammer interface { EnsureRestrictedACL(ctx, name string, destinations []string) error; SetInstanceNICACLs(ctx, ref string, acls []string) error; VerifyInstanceNICACLs(ctx, ref string, expected []string) error; RecordPolicyDenied(id domain.InstanceID); PolicyDenied(id) uint64; SetResolution(id, domain.AllowlistResolution); Resolution(id) domain.AllowlistResolution }`
  - Or keep methods on `*incus.Adapter` and define:
  - `type Applicator struct { Resolver *dnsproxy.AllowlistResolver; Runtime ACLRuntime }`
  - `func (a *Applicator) Apply(ctx context.Context, instance domain.Instance, profile domain.EgressProfile) error`
  - `NetworkPolicyStatus` returns stored resolution (not always `idle`)
  - Updated `NICACLs(mode, restrictedACLName...)` usage: restricted always passes `networkpolicy.RestrictedACLName(string(instance.ID))`

**Preferred adapter change** (keep daemon wiring simple):

```go
// PolicyApply carries profile-derived inputs for one instance apply.
type PolicyApply struct {
    Instance     domain.Instance
    Mode         domain.EgressMode
    Destinations []string // literals IP/CIDR + resolved addresses as strings
    Hostnames    []string // for resolution state only
}

func (a *Adapter) ApplyNetworkPolicy(ctx context.Context, apply PolicyApply) error
```

Resolution + DNS happen in `internal/app/egress.Applicator` before calling `ApplyNetworkPolicy` with already-resolved destination strings. Adapter still: EnsureRestrictedACL (restricted), set NIC ACLs, verify, store resolution provided by applicator via `a.SetAllowlistResolution(instanceID, resolution)`.

- [ ] **Step 1: Write failing apply tests (fake runtime + fake resolver)**

```go
func TestApplyRestrictedWithHostnameProgramsACL(t *testing.T) {
    fake := &fakeACLRuntime{}
    resolver := dnsproxy.NewAllowlistResolver(dnsproxy.Config{
        Resolver: stubResolver{"packages.example.com": {Addresses: []netip.Addr{netip.MustParseAddr("203.0.113.10")}, TTL: time.Minute}},
        MinTTL: time.Second, MaxTTL: 5 * time.Minute,
    })
    app := egress.NewApplicator(resolver, fake)
    err := app.Apply(context.Background(), domain.Instance{
        ID: "inst-1", RuntimeRef: "ref-1", EgressMode: domain.EgressRestricted,
    }, domain.EgressProfile{
        Mode: domain.EgressRestricted,
        AllowedDestinationsJSON: []byte(`["packages.example.com"]`),
    })
    if err != nil {
        t.Fatal(err)
    }
    if !fake.ensuredACL {
        t.Fatal("expected EnsureRestrictedACL")
    }
    want := []string{"openbox-default-deny", networkpolicy.RestrictedACLName("inst-1")}
    if !reflect.DeepEqual(fake.nicACLs, want) {
        t.Fatalf("nic ACLs = %v", fake.nicACLs)
    }
}

func TestApplyRebindingHostnameFailsClosed(t *testing.T) {
    // resolver returns only 10.0.0.1 → Apply error, resolution failed includes hostname
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/egress/ -count=1`  
Expected: FAIL

- [ ] **Step 3: Implement Applicator + adapter updates**

`apply.go` responsibilities:
1. Unmarshal/split profile destinations
2. Set resolution `pending` for hostnames
3. Resolve each hostname; on failure mark `failed` and return error
4. Merge literals + resolved addrs
5. Call runtime apply
6. Set resolution `resolved` or `idle` if no hostnames

Update `Adapter.ApplyNetworkPolicy` to accept destinations / use `PolicyApply`, call `EnsureRestrictedACL` for restricted (even if destinations empty), pass restricted ACL name into `NICACLs` and `VerifyNetworkPolicy`.

Update `instances.NetworkPolicy` interface to match. Fix all compile breaks in tests with fakes.

- [ ] **Step 4: Run targeted tests**

Run: `go test ./internal/app/egress/ ./internal/runtime/incus/ ./internal/app/instances/ -count=1`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/egress/ internal/runtime/incus/ internal/app/instances/
git commit -m "$(cat <<'EOF'
feat: apply restricted egress allowlists via host DNS

EOF
)"
```

---

### Task 4: Profile service, seed, fan-out, instance attach

**Files:**
- Create: `internal/app/egress/service.go`
- Create: `internal/app/egress/service_test.go`
- Modify: `internal/app/instances/service.go` (CreateInput, create path, AttachEgressProfile)
- Modify: `cmd/openboxd/daemon.go` (construct egress service, seed, wire applicator)

**Interfaces:**
- Consumes: store profile/instance methods, `Applicator.Apply`
- Produces:
  - `type Service struct { ... }`
  - `func New(store Store, applicator *Applicator, instances InstanceLister, opts Options) (*Service, error)`
  - `EnsureSeeds(ctx) error`
  - `List/Get/Create/Update/Delete` profiles
  - `Update` returns `(domain.EgressProfile, []ApplyError, error)` where `ApplyError` is `{InstanceID, Message string}`
  - `DefaultProfileID(kind domain.InstanceKind) domain.EgressProfileID`
  - `instances.CreateInput.EgressProfileID` optional; if empty, set from kind
  - `instances.Service.AttachEgressProfile(ctx, ownerID, instanceID, profileID) (domain.Instance, error)`

- [ ] **Step 1: Write failing service tests**

```go
func TestUpdateProfileFanOutKeepsProfileOnInstanceFailure(t *testing.T) {
    // profile update changes destinations
    // applicator fails for instance A, succeeds for B
    // GetEgressProfile shows new destinations
    // apply_errors contains A
}

func TestCreateInstanceAttachesDefaultRestrictedForSandbox(t *testing.T) {
    // create sandbox without profile id → egress-restricted, mode restricted
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app/egress/ ./internal/app/instances/ -run 'FanOut|AttachesDefault' -count=1`  
Expected: FAIL

- [ ] **Step 3: Implement service + wire create/attach**

On create (before/at persistence):
```go
if input.EgressProfileID == "" {
    input.EgressProfileID = egress.DefaultProfileID(input.Kind)
}
profile, err := store.GetEgressProfile(ctx, input.EgressProfileID)
instance.EgressProfileID = profile.ID
instance.EgressMode = profile.Mode
```

On apply after runtime start: load profile by `instance.EgressProfileID`, call `applicator.Apply`.

`AttachEgressProfile`: update DB profile id + mode, then `applicator.Apply` if `RuntimeRef != ""`.

Daemon: after store open, `egressService.EnsureSeeds`; pass applicator into instances options.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/app/egress/ ./internal/app/instances/ ./cmd/openboxd/ -count=1`  
Expected: PASS (or compile-only for openboxd if no tests)

- [ ] **Step 5: Commit**

```bash
git add internal/app/egress/ internal/app/instances/ cmd/openboxd/daemon.go
git commit -m "$(cat <<'EOF'
feat: manage egress profiles with attach and re-apply fan-out

EOF
)"
```

---

### Task 5: OpenAPI and HTTP handlers

**Files:**
- Modify: `api/openapi.yaml`
- Modify: `internal/httpapi/handler.go` (route dispatch)
- Create: `internal/httpapi/egress_profile_handlers.go`
- Modify: `internal/httpapi/handler.go` / instance mappers for `egress_profile_id`
- Regenerate: `go generate ./internal/httpapi/generated/...`
- Test: `internal/httpapi/egress_profile_handlers_test.go` and/or extend `integration_test.go`

**Interfaces:**
- Consumes: `egress.Service`
- Produces REST:
  - `GET/POST /v1/network/egress-profiles`
  - `GET/PATCH/DELETE /v1/network/egress-profiles/{profile_id}`
  - `PUT /v1/instances/{instance_id}/network/egress-profile` body `{ "egress_profile_id": "..." }`
  - Instance schema: `egress_profile_id`, `egress_profile_name` (optional convenience), existing `network_policy`
  - CreateInstanceRequest: optional `egress_profile_id`
  - PATCH profile response may include `apply_errors: [{instance_id, message}]`

- [ ] **Step 1: Extend OpenAPI schemas and paths**

Follow `/v1/routes` and `/v1/pi-profiles` CSRF patterns for mutating verbs. Add `EgressProfile` schema:

```yaml
EgressProfile:
  type: object
  required: [id, name, mode, allowed_destinations, system, created_at, updated_at]
  properties:
    id: { type: string }
    name: { type: string }
    mode: { type: string, enum: [standard, restricted] }
    allowed_destinations:
      type: array
      items: { type: string }
    system: { type: boolean }
    attached_instance_count: { type: integer }
    created_at: { type: string, format: date-time }
    updated_at: { type: string, format: date-time }
ApplyError:
  type: object
  required: [instance_id, message]
  properties:
    instance_id: { type: string }
    message: { type: string }
```

- [ ] **Step 2: Regenerate types**

Run: `go generate ./internal/httpapi/generated/`  
Expected: `types.go` / `schema_hash.go` update; `go test ./internal/httpapi/ -run Contract -count=1` passes after handlers exist

- [ ] **Step 3: Write failing handler tests**

```go
func TestEgressProfileCreateAndAttach(t *testing.T) {
    // POST profile, create sandbox, PUT attach, GET instance shows profile id + network_policy
}
```

- [ ] **Step 4: Implement handlers and wire into API server constructor**

Mirror `pi_profile_handlers.go` auth/CSRF. Map `allowed_destinations` ↔ JSON blob on domain profile.

- [ ] **Step 5: Run HTTP tests**

Run: `go test ./internal/httpapi/ -count=1`  
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add api/openapi.yaml internal/httpapi/
git commit -m "$(cat <<'EOF'
feat: expose egress profile API and instance attach

EOF
)"
```

---

### Task 6: CLI `openbox network`

**Files:**
- Create: `cmd/openbox/network.go`
- Create: `cmd/openbox/network_test.go`
- Modify: `cmd/openbox/main.go` (usage + switch)
- Modify: `internal/client/` (methods for profile CRUD + attach)

**Interfaces:**
- Consumes: HTTP client
- Produces:
  - `openbox network profiles ls|show|create|edit|delete`
  - `openbox network attach <instance> <profile>`
  - Inspect already prints policy; add profile name/id lines in `printInstanceStatus`

- [ ] **Step 1: Write failing CLI test**

```go
func TestNetworkProfilesListJSON(t *testing.T) {
    // httptest returns profile list; runNetwork(... "profiles", "ls"); assert names
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/openbox/ -run TestNetworkProfilesListJSON -count=1`  
Expected: FAIL

- [ ] **Step 3: Implement client methods + `runNetwork`**

Pattern from `cmd/openbox/route.go`. Human output columns: NAME, MODE, SYSTEM, DESTINATIONS(count), ATTACHED.

- [ ] **Step 4: Run CLI tests**

Run: `go test ./cmd/openbox/ -count=1`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/openbox/ internal/client/
git commit -m "$(cat <<'EOF'
feat: add openbox network CLI for egress profiles

EOF
)"
```

---

### Task 7: Dashboard Network policy page

**Files:**
- Create: `web/src/pages/NetworkPolicy.tsx`
- Create: `web/src/pages/NetworkPolicy.test.tsx` (if test pattern exists; else extend InstancePage test)
- Modify: `web/src/pages/ConsolePage.tsx` (view + nav)
- Modify: `web/src/pages/InstancePage.tsx` (profile + ACLs + resolution + switch)
- Modify: `web/src/api/client.ts` (types + fetch helpers)
- Modify: `web/src/pages/Sandbox.tsx` — stop hardcoding `egressPolicy="default"`; pass real mode/profile label
- Run: `cd web && pnpm test` / `pnpm run generate` if console assets embed required for Go tests

**Interfaces:**
- Consumes: `/v1/network/egress-profiles`, attach endpoint
- Produces: console view `{ kind: "network-policy" }`; instance detail shows `egressProfileId`, `networkPolicy.*`

- [ ] **Step 1: Extend client types**

```typescript
export interface EgressProfile {
  id: string;
  name: string;
  mode: "standard" | "restricted";
  allowedDestinations: string[];
  system: boolean;
  attachedInstanceCount?: number;
}
```

Add `listEgressProfiles`, `createEgressProfile`, `updateEgressProfile`, `deleteEgressProfile`, `attachEgressProfile`.

- [ ] **Step 2: Write failing UI test (match Sandbox.test / InstancePage patterns)**

Assert Network policy page renders profile names; instance detail shows resolution state when present.

- [ ] **Step 3: Implement `NetworkPolicy.tsx` + wire ConsolePage nav**

Keep existing console visual language. Page jobs: list profiles, edit destinations (textarea of one entry per line), create custom profile, delete when allowed. Instance detail: show profile, mode, ACLs, resolution, denied flows; select to switch profile.

- [ ] **Step 4: Run web tests and regenerate embedded assets if repo requires**

Run: `cd web && pnpm test`  
Then if CI embeds UI: `cd web && pnpm run generate` and include `internal/assets/static/` changes in commit.

- [ ] **Step 5: Commit**

```bash
git add web/ internal/assets/static/
git commit -m "$(cat <<'EOF'
feat: add Network policy console page and instance policy details

EOF
)"
```

---

### Task 8: Docs, live matrix hook, slice plan checkbox sync

**Files:**
- Modify: `docs/security/networking.md`
- Create: `docs/operators/egress-profiles.md`
- Modify: `docs/plans/19-egress-allowlists-and-network-ux.md` (checkboxes → complete, status)
- Create or modify: `internal/runtime/incus/egress_matrix_integration_test.go` (opt-in)
- Force-add docs if `/docs` gitignored: `git add -f docs/...`

**Interfaces:**
- Consumes: finished behavior from Tasks 1–7
- Produces: operator workflow doc; security doc profiles section; opt-in test skipped without env

- [ ] **Step 1: Write opt-in live test skeleton**

```go
func TestLiveEgressAllowlistMatrix(t *testing.T) {
    socket := os.Getenv("OPENBOX_INCUS_TEST_SOCKET")
    if socket == "" {
        t.Skip("set OPENBOX_INCUS_TEST_SOCKET to run live egress matrix")
    }
    // container + VM: restricted empty denies internet; allowlist IP allows only that dest
}
```

- [ ] **Step 2: Run default unit path (skip)**

Run: `go test ./internal/runtime/incus/ -run TestLiveEgressAllowlistMatrix -count=1`  
Expected: SKIP

- [ ] **Step 3: Update docs**

Document: seed profiles, approve destinations, attach/switch, fan-out `apply_errors`, fail-closed resolve, no IP leakage in inspect.

- [ ] **Step 4: Mark Slice 19 plan tasks complete; set `status: complete` when acceptance gate met**

- [ ] **Step 5: Full verification**

Run:
```bash
go test ./internal/domain/ ./internal/persistence/sqlite/ ./internal/app/egress/ ./internal/app/instances/ ./internal/runtime/incus/ ./internal/httpapi/ ./cmd/openbox/ ./internal/dnsproxy/ -count=1
cd web && pnpm test
```
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add -f docs/security/networking.md docs/operators/egress-profiles.md docs/plans/19-egress-allowlists-and-network-ux.md
git add internal/runtime/incus/egress_matrix_integration_test.go
git commit -m "$(cat <<'EOF'
docs: document egress profiles and finish Slice 19 plan

EOF
)"
```

---

## Self-review checklist (author)

| Spec requirement | Task |
|------------------|------|
| System profiles, editable, seeded defaults | 1, 2, 4 |
| Instance FK + kind defaults | 1, 2, 4 |
| Profile owns mode | 4 |
| dnsproxy on apply + rebinding fail-closed | 3 |
| EnsureRestrictedACL + NIC stack | 3 |
| Edit fan-out keep profile / apply_errors | 4, 5 |
| Switch profile anytime | 4, 5, 6, 7 |
| Delete in-use / system rejected | 2, 5 |
| Resolution state on inspect | 3, 5, 6, 7 |
| API + CLI + dashboard | 5, 6, 7 |
| Operator + security docs | 8 |
| Opt-in live Incus matrix | 8 |
| Empty restricted still locked down | 3, 8 |

No TBD placeholders remain in task steps. Types use `EgressProfileID`, `Applicator.Apply`, `PolicyApply` / adapter methods consistently across tasks.
