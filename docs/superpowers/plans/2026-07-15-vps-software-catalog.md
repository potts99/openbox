# VPS Software Catalog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Collapse Devbox into VPS, add a curated software catalog (Pi first) installable at create and later, and remove the Launch Pi button so users run tools from the normal terminal.

**Architecture:** OpenBox-owned package catalog (data recipes + exact pins) drives guest installs via Incus `Exec` after the instance is ready. Instance software state is persisted and exposed on the instance API. Kind `devbox` is hard-deleted from the product surface (no migration shims — no users yet).

**Tech Stack:** Go (domain, sqlite, httpapi, incus exec), OpenAPI, React/Vite console, existing operations worker patterns.

## Global Constraints

- No `devbox` kind in create/API after this work; hard cut, no runtime mappers.
- Catalog recipes are data + argv only — no caller-supplied shell strings, no untrusted remote script steps.
- Package pins are exact versions (reuse Devbox pin rules for `pi`).
- Install via managed-instance Incus `Exec` only (never host shell).
- No Launch Pi UI; terminal is the only run surface.
- Protect/clone allowed on VPS.
- YAGNI: install-only in v1 (no uninstall endpoint yet).

## File map

| Path | Role |
|---|---|
| `internal/software/` | Catalog types, `pi` package definition, recipe validation |
| `internal/persistence/migrations/008_instance_software.sql` | `instance_software` table; drop `devbox` from kind CHECK |
| `internal/persistence/sqlite/` | CRUD for software rows; instance mapping |
| `internal/domain/` | Remove `KindDevbox`; software status types if needed |
| `internal/app/instances/` | Create `packages`, `InstallSoftware`, protect on VPS |
| `internal/app/clones/` | Secrets warning based on Pi software / paths, not Devbox kind |
| `internal/httpapi/` | Catalog + install routes; instance JSON `software` |
| `api/openapi.yaml` | Enums + endpoints |
| `web/src/` | Software panel; remove Launch Pi; create checkbox |
| `docs/operators/`, `docs/development/` | Reflect new model |

---

### Task 1: Software catalog package (`pi`)

**Files:**
- Create: `internal/software/catalog.go`
- Create: `internal/software/catalog_test.go`
- Create: `internal/software/pi.go` (pins/recipe sourced from existing Devbox definition)

**Interfaces:**
- Produces: `type Package struct { ID, Name, Description string; Pins []Pin; Install, Verify [][]string }`
- Produces: `func DefaultCatalog() Catalog`, `func (c Catalog) Get(id string) (Package, bool)`, `func (p Package) Validate() error`

- [ ] **Step 1: Write failing catalog tests**

```go
func TestDefaultCatalogIncludesPiWithExactPins(t *testing.T) {
	t.Parallel()
	cat := software.DefaultCatalog()
	pkg, ok := cat.Get("pi")
	if !ok {
		t.Fatal("missing pi")
	}
	if err := pkg.Validate(); err != nil {
		t.Fatal(err)
	}
	if pkg.ID != "pi" || len(pkg.Install) == 0 || len(pkg.Verify) == 0 {
		t.Fatalf("%+v", pkg)
	}
}
```

- [ ] **Step 2: Run test — expect fail (package missing)**

Run: `go test ./internal/software/ -count=1`

- [ ] **Step 3: Implement catalog**

Load pins from `images.LoadDevboxDefinition()` (or duplicate pin constants once — prefer reuse). `Install`/`Verify` are argv lists, e.g. verify `{"pi","--version"}`, `{"tmux","-V"}`. Reject range versions in `Validate` like `images.DevboxDefinition`.

- [ ] **Step 4: Run tests — expect pass**

Run: `go test ./internal/software/ -count=1`

- [ ] **Step 5: Commit**

```bash
git add internal/software/
git commit -m "feat: add curated software catalog with pi package"
```

---

### Task 2: Persistence — `instance_software` + drop `devbox` CHECK

**Files:**
- Create: `internal/persistence/migrations/008_instance_software.sql`
- Modify: `internal/persistence/sqlite/repositories.go` (scan/write helpers)
- Create: `internal/persistence/sqlite/software.go` (or extend repositories)
- Test: `internal/persistence/sqlite/software_test.go`
- Modify: `internal/domain/types.go` — remove `KindDevbox`
- Modify: `internal/domain/instance.go` — validation only `sandbox`|`vps`

**Notes:** SQLite CHECK changes require table rebuild. Migration should:
1. Create `instance_software (...)`.
2. Rebuild `instances` without `devbox` in CHECK (copy rows where `kind != 'devbox'`; leftover local `devbox` rows may be deleted in migration — acceptable hard cut).

- [ ] **Step 1: Write failing store test for upsert/list software**

```go
func TestInstanceSoftwareUpsertAndList(t *testing.T) {
	// create vps instance, UpsertSoftware(pi, installed), ListSoftware returns one row
}
```

- [ ] **Step 2: Add migration `008_instance_software.sql` + store methods**

Schema sketch:

```sql
CREATE TABLE instance_software (
  instance_id TEXT NOT NULL REFERENCES instances(id),
  owner_id TEXT NOT NULL,
  package_id TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('absent','pending','installed','failed')),
  version TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL,
  PRIMARY KEY (instance_id, package_id)
);
```

- [ ] **Step 3: Remove `KindDevbox` from domain; fix compile breaks by switching tests to `KindVPS`**

- [ ] **Step 4: `go test ./internal/domain/ ./internal/persistence/sqlite/ -count=1`**

- [ ] **Step 5: Commit**

```bash
git commit -m "feat: persist instance software and drop devbox kind"
```

---

### Task 3: Install executor (guest recipe via Exec)

**Files:**
- Create: `internal/software/install.go`
- Create: `internal/software/install_test.go`

**Interfaces:**
- Consumes: `software.Package`, runtime `Exec(ctx, ExecRequest) (ExecResult, error)`
- Produces: `func Install(ctx, execer, runtimeRef string, pkg Package) error` — runs install argv steps then verify; non-zero exit fails

- [ ] **Step 1: Failing test with fake execer recording commands**

```go
func TestInstallRunsPinsThenVerify(t *testing.T) {
	// stub returns exit 0; assert verify commands invoked
}
func TestInstallFailsOnVerify(t *testing.T) {
	// verify exit 1 → error
}
```

- [ ] **Step 2: Implement `Install` — sequential Exec, cwd `/`, env `DEBIAN_FRONTEND=noninteractive` where needed via argv (`apt-get -y`)**

Keep steps as explicit argv in the `pi` package definition (e.g. `apt-get`, `install`, `-y`, `tmux=...`) rather than `sh -c`.

- [ ] **Step 3: Tests pass**

- [ ] **Step 4: Commit**

```bash
git commit -m "feat: run software catalog recipes through guest exec"
```

---

### Task 4: Instance service — create packages + InstallSoftware + protect on VPS

**Files:**
- Modify: `internal/app/instances/service.go`
- Modify: `internal/app/instances/service_test.go`
- Modify: `internal/app/clones/service.go` (secrets warning: Pi software or paths, not `KindDevbox`)

**Interfaces:**
- Extends `CreateInput` with `Packages []string`
- Produces: `InstallSoftware(ctx, owner, id, packageID string) (domain.Instance /* or op */, error)`
- Change `SetProtection`: allow `KindVPS` (and reject sandbox)

Behavior:
- Create validates package IDs against catalog; after create success path once **ready**, mark `pending` and run install (prefer enqueue durable operation type e.g. `instance.software.install` if operations pattern fits; else synchronous post-ready hook in worker — match existing create completion style).
- `InstallSoftware` on running instance: set `pending`, exec recipe, set `installed`/`failed`.

- [ ] **Step 1: Tests — protect VPS ok; protect sandbox fails; create with `packages:["pi"]` records pending/installed via fake runtime Exec**

- [ ] **Step 2: Implement service changes; fix clone warning condition**

- [ ] **Step 3: `go test ./internal/app/instances/ ./internal/app/clones/ -count=1`**

- [ ] **Step 4: Commit**

```bash
git commit -m "feat: install catalog software on VPS create and demand"
```

---

### Task 5: HTTP API + OpenAPI

**Files:**
- Modify: `api/openapi.yaml` — remove `devbox` from kind enums; add `SoftwarePackage`, instance `software`, `packages` on create, `GET /v1/software`, `POST /v1/instances/{id}/software/{package_id}/install`
- Modify: `internal/httpapi/` handlers + `mapInstance` to include software
- Regenerate or hand-update `internal/httpapi/generated/types.go` / client types per repo convention
- Tests: `internal/httpapi/` handler tests

- [ ] **Step 1: OpenAPI + failing contract/handler tests for list catalog and install**

- [ ] **Step 2: Wire routes in handler mux; auth same as instance mutations**

- [ ] **Step 3: `go test ./internal/httpapi/ ./internal/client/ -count=1`**

- [ ] **Step 4: Commit**

```bash
git commit -m "feat: expose software catalog and install API"
```

---

### Task 6: Console UI — Software panel; remove Launch Pi

**Files:**
- Delete or stop using: `web/src/components/LaunchPi.tsx`, `launchPiAvailable.ts` (+ tests)
- Modify: `web/src/pages/InstancePage.tsx` — Software section; Terminal only
- Modify: `web/src/pages/ConsolePage.tsx` — drop `launchPi` from terminal view state
- Modify: `web/src/api/client.ts` — `software`, `listSoftware`, `installSoftware`, create `packages`
- Modify: create UI if present (checkbox “Pi coding agent”)
- Tests: InstancePage / client tests updated (no Launch Pi; install button)

- [ ] **Step 1: Failing UI test — Launch Pi absent; Software Install present for VPS**

- [ ] **Step 2: Implement UI + client methods**

- [ ] **Step 3: `cd web && npm test -- --run` (or project’s vitest command)**

- [ ] **Step 4: Rebuild embedded assets if required by repo (`go generate` / web build into `internal/assets`)**

- [ ] **Step 5: Commit**

```bash
git commit -m "feat: software panel and remove Launch Pi from console"
```

---

### Task 7: Docs + final kind sweep

**Files:**
- Modify: `docs/operators/pi-profile-and-launcher.md`, `docs/development/pi-profile-and-launcher.md`, `docs/operators/images-snapshots-cloning.md`, `docs/plans/12-pi-profile-and-launcher.md` (status note)
- Grep sweep: remaining `KindDevbox` / `kind: "devbox"` in tests → `vps`

- [ ] **Step 1: Update docs to VPS + catalog + terminal-only Pi**

- [ ] **Step 2: `rg 'KindDevbox|"devbox"' --glob '*.{go,tsx,ts,yaml,md}'` and clear product references (tests use `vps`)**

- [ ] **Step 3: Full test**

Run: `go test ./...` and web unit tests

- [ ] **Step 4: Commit**

```bash
git commit -m "docs: document VPS software catalog; remove devbox references"
```

---

## Spec coverage check

| Spec item | Task |
|---|---|
| Catalog with Pi pins/recipes | 1 |
| Persist software state | 2 |
| Hard-cut `devbox` | 2, 5, 7 |
| Guest Exec install | 3, 4 |
| Create `packages` + on-demand install | 4, 5 |
| Protect/clone on VPS | 4 |
| API | 5 |
| UI Software; no Launch Pi | 6 |
| Docs | 7 |
| No uninstall in v1 | (omitted — follow-up) |

## Placeholder scan

None intentional; recipes must be concrete argv in Task 1/3.
