# Herdr Software Catalog Package Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add curated `herdr` to the OpenBox software catalog, installed via host-fetched GitHub release pins (sha256-verified) and guest `Exec`+`Stdin`, runnable from the normal Terminal.

**Architecture:** Extend `internal/software` with a `github-release` pin manager. `Install` detects release pins, downloads the arch-matched asset on the host, verifies digest, then writes `/usr/local/bin/herdr` through argv-only guest steps (`tee` → `chmod` → `mv`) and runs `herdr --version`. Catalog list / create `packages` / install API pick it up automatically once registered in `DefaultCatalog()`.

**Tech Stack:** Go (`internal/software`, instances service), existing Incus `Exec`+`Stdin`, HTTP fetch on host, OpenAPI/catalog already in place.

## Global Constraints

- Exact pins only; initial Herdr version `0.7.4`.
- Linux assets only: `herdr-linux-x86_64`, `herdr-linux-aarch64` with pinned sha256.
- Never use `curl|sh` or guest `curl`/`wget`/raw `https` recipe steps.
- Host fetch + sha256 verify; fail closed on mismatch/empty/unsupported arch.
- Guest install path: `/usr/local/bin/herdr` via temp `herdr.openbox-tmp`.
- No Launch Herdr UI; no tmux replacement; no uninstall; no sandbox software scope.
- YAGNI: reusable release helper only as needed for `herdr` (not a generic multi-repo marketplace).

## File map

| Path | Role |
|---|---|
| `internal/software/catalog.go` | Allow `github-release` pins; validate assets; catalog list order |
| `internal/software/release.go` | Release asset types, URL builder, fetch+digest verify |
| `internal/software/install.go` | Host-fetch install path + stdin write steps |
| `internal/software/herdr.go` | Curated `herdr` package pins (v0.7.4 digests) |
| `internal/software/*_test.go` | Catalog + install + digest failure tests |
| `internal/app/instances/service.go` | Pass architecture into `Install`; record release pin version |
| `internal/httpapi/handler_test.go` | Catalog includes `herdr` |
| `docs/operators/`, `docs/development/` | Herdr package + pin bump notes |

---

### Task 1: `github-release` pin validation

**Files:**
- Modify: `internal/software/catalog.go`
- Modify: `internal/software/catalog_test.go`
- Create: `internal/software/release.go` (types only in this task)

**Interfaces:**
- Produces:
  ```go
  type ReleaseAsset struct {
      Arch     string // "x86_64" or "aarch64"
      Filename string
      SHA256   string // lowercase hex, no sha256: prefix
  }
  // Pin gains:
  //   Assets []ReleaseAsset // required when Manager == "github-release"; Name is "owner/repo"
  ```
- Produces: `validatePin` accepts `github-release` when `Name` matches `owner/repo`, version exact, and assets cover both `x86_64` and `aarch64` with 64-char hex digests
- Produces: `Package.Validate` allows empty `Install` when the package has at least one `github-release` pin (verify still required)

- [ ] **Step 1: Write failing tests**

```go
func TestValidateAcceptsGitHubReleasePins(t *testing.T) {
	t.Parallel()
	pkg := software.Package{
		ID:   "herdr",
		Name: "Herdr",
		Pins: []software.Pin{{
			Manager: "github-release",
			Name:    "ogulcancelik/herdr",
			Version: "0.7.4",
			Assets: []software.ReleaseAsset{
				{Arch: "x86_64", Filename: "herdr-linux-x86_64", SHA256: strings.Repeat("a", 64)},
				{Arch: "aarch64", Filename: "herdr-linux-aarch64", SHA256: strings.Repeat("b", 64)},
			},
		}},
		Verify: [][]string{{"herdr", "--version"}},
	}
	if err := pkg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsGitHubReleaseMissingArch(t *testing.T) {
	t.Parallel()
	pkg := software.Package{
		ID:   "herdr",
		Name: "Herdr",
		Pins: []software.Pin{{
			Manager: "github-release",
			Name:    "ogulcancelik/herdr",
			Version: "0.7.4",
			Assets: []software.ReleaseAsset{
				{Arch: "x86_64", Filename: "herdr-linux-x86_64", SHA256: strings.Repeat("a", 64)},
			},
		}},
		Verify: [][]string{{"herdr", "--version"}},
	}
	if err := pkg.Validate(); err == nil {
		t.Fatal("expected missing aarch64 rejection")
	}
}

func TestValidateStillRejectsRemoteScriptSteps(t *testing.T) {
	t.Parallel()
	pkg := software.Package{
		ID: "bad", Name: "Bad",
		Pins:    []software.Pin{{Manager: "apt", Name: "tmux", Version: "3.4-1"}},
		Install: [][]string{{"curl", "-fsSL", "https://example.com/install.sh"}},
		Verify:  [][]string{{"true"}},
	}
	if err := pkg.Validate(); err == nil {
		t.Fatal("expected remote script rejection")
	}
}
```

- [ ] **Step 2: Run tests — expect fail**

Run: `go test ./internal/software/ -count=1 -run 'TestValidateAcceptsGitHubReleasePins|TestValidateRejectsGitHubReleaseMissingArch'`

Expected: FAIL (unknown field `Assets` and/or unsupported manager)

- [ ] **Step 3: Implement types + validation**

In `release.go`:

```go
type ReleaseAsset struct {
	Arch     string
	Filename string
	SHA256   string
}
```

In `catalog.go` extend `Pin` with `Assets []ReleaseAsset`. Update `validatePin`:

- Allow `manager == "github-release"`.
- Require `Name` of form `owner/repo` (one `/`, non-empty sides, no spaces).
- Require both arches present with non-empty `Filename` and `SHA256` matching `^[0-9a-f]{64}$`.
- Keep apt/npm rules unchanged.

Update `Validate` so `len(Install)==0` is allowed iff any pin has `Manager == "github-release"`.

- [ ] **Step 4: Run tests — expect pass**

Run: `go test ./internal/software/ -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/software/catalog.go internal/software/catalog_test.go internal/software/release.go
git commit -m "$(cat <<'EOF'
feat: allow github-release pins in software catalog

EOF
)"
```

---

### Task 2: Host fetch + guest stdin install path

**Files:**
- Modify: `internal/software/release.go`
- Modify: `internal/software/install.go`
- Modify: `internal/software/install_test.go`

**Interfaces:**
- Consumes: `Pin` / `ReleaseAsset` from Task 1; `runtimeapi.ExecRequest.Stdin`
- Produces:
  ```go
  type ReleaseFetcher interface {
      Fetch(ctx context.Context, url string) ([]byte, error)
  }
  type InstallOptions struct {
      Architecture string // "x86_64" or "aarch64"
      Fetcher      ReleaseFetcher // nil → default HTTP fetcher
  }
  func ReleaseURL(repo, version, filename string) string
  func Install(ctx context.Context, execer GuestExecer, runtimeRef string, pkg Package, opts InstallOptions) error
  ```
- Behavior: if pkg has a `github-release` pin, ignore empty `Install` argv list; fetch+verify; exec `tee /usr/local/bin/<id>.openbox-tmp` with stdin bytes; `chmod 0755` temp; `mv` temp → `/usr/local/bin/<id>`; then run `Verify`. apt/npm packages keep prior argv-only path (pass empty Architecture ok).

- [ ] **Step 1: Write failing install tests**

```go
type mapFetcher map[string][]byte

func (m mapFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	body, ok := m[url]
	if !ok {
		return nil, fmt.Errorf("missing %s", url)
	}
	return body, nil
}

type recordingExecer struct {
	commands [][]string
	stdins   [][]byte
	// ... existing result maps ...
}

func (e *recordingExecer) Exec(_ context.Context, req runtimeapi.ExecRequest) (runtimeapi.ExecResult, error) {
	e.commands = append(e.commands, append([]string{}, req.Command...))
	if req.Stdin != nil {
		b, _ := io.ReadAll(req.Stdin)
		e.stdins = append(e.stdins, b)
	} else {
		e.stdins = append(e.stdins, nil)
	}
	// ... existing exit handling ...
}

func TestInstallGitHubReleaseWritesBinaryAndVerifies(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256([]byte("herdr-bytes"))
	digest := hex.EncodeToString(sum[:])
	pkg := software.Package{
		ID:   "herdr",
		Name: "Herdr",
		Pins: []software.Pin{{
			Manager: "github-release",
			Name:    "ogulcancelik/herdr",
			Version: "0.7.4",
			Assets: []software.ReleaseAsset{
				{Arch: "x86_64", Filename: "herdr-linux-x86_64", SHA256: digest},
				{Arch: "aarch64", Filename: "herdr-linux-aarch64", SHA256: strings.Repeat("b", 64)},
			},
		}},
		Verify: [][]string{{"herdr", "--version"}},
	}
	url := software.ReleaseURL("ogulcancelik/herdr", "0.7.4", "herdr-linux-x86_64")
	execer := &recordingExecer{}
	err := software.Install(context.Background(), execer, "ref-1", pkg, software.InstallOptions{
		Architecture: "x86_64",
		Fetcher:      mapFetcher{url: []byte("herdr-bytes")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(execer.commands) < 4 {
		t.Fatalf("commands=%v", execer.commands)
	}
	if strings.Join(execer.commands[0], " ") != "tee /usr/local/bin/herdr.openbox-tmp" {
		t.Fatalf("tee=%v", execer.commands[0])
	}
	if string(execer.stdins[0]) != "herdr-bytes" {
		t.Fatalf("stdin=%q", execer.stdins[0])
	}
	if strings.Join(execer.commands[1], " ") != "chmod 0755 /usr/local/bin/herdr.openbox-tmp" {
		t.Fatalf("chmod=%v", execer.commands[1])
	}
	if strings.Join(execer.commands[2], " ") != "mv /usr/local/bin/herdr.openbox-tmp /usr/local/bin/herdr" {
		t.Fatalf("mv=%v", execer.commands[2])
	}
	if strings.Join(execer.commands[3], " ") != "herdr --version" {
		t.Fatalf("verify=%v", execer.commands[3])
	}
}

func TestInstallGitHubReleaseRejectsDigestMismatch(t *testing.T) {
	t.Parallel()
	pkg := /* same shape with SHA256 all "a"s for x86_64 */
	url := software.ReleaseURL("ogulcancelik/herdr", "0.7.4", "herdr-linux-x86_64")
	err := software.Install(context.Background(), &recordingExecer{}, "ref-1", pkg, software.InstallOptions{
		Architecture: "x86_64",
		Fetcher:      mapFetcher{url: []byte("herdr-bytes")},
	})
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("error=%v", err)
	}
}

func TestInstallGitHubReleaseRejectsUnsupportedArch(t *testing.T) {
	t.Parallel()
	// valid pkg; opts.Architecture = "riscv64"
	err := software.Install(...)
	if err == nil || !strings.Contains(err.Error(), "architecture") {
		t.Fatalf("error=%v", err)
	}
}
```

Also update existing `TestInstallRunsPinsThenVerify` / fail tests to pass `software.InstallOptions{}` so they still compile.

- [ ] **Step 2: Run tests — expect fail**

Run: `go test ./internal/software/ -count=1 -run 'TestInstallGitHubRelease'`

Expected: FAIL (signature / missing helpers)

- [ ] **Step 3: Implement fetch + install**

```go
func ReleaseURL(repo, version, filename string) string {
	return "https://github.com/" + repo + "/releases/download/v" + version + "/" + filename
}

type httpReleaseFetcher struct {
	client *http.Client
	limit  int64 // e.g. 64 << 20
}

func (f httpReleaseFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	// GET with ctx; reject non-200; io.LimitReader; read all
}
```

`Install`:

1. `pkg.Validate()`.
2. If github-release pin present:
   - require `opts.Architecture`
   - select matching asset or error
   - `fetcher := opts.Fetcher`; if nil use default HTTP fetcher
   - `body, err := fetcher.Fetch(ctx, ReleaseURL(...))`
   - sha256 compare (constant time); empty body fails
   - exec tee with `Stdin: bytes.NewReader(body)`
   - exec chmod; exec mv
   - run verify steps
   - return
3. Else existing argv install+verify path (unchanged).

Binary destination: `/usr/local/bin/` + `pkg.ID`. Temp: that path + `.openbox-tmp`.

- [ ] **Step 4: Run tests — expect pass**

Run: `go test ./internal/software/ -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/software/release.go internal/software/install.go internal/software/install_test.go
git commit -m "$(cat <<'EOF'
feat: install github-release packages via host fetch and guest stdin

EOF
)"
```

---

### Task 3: Curated `herdr` package (v0.7.4)

**Files:**
- Create: `internal/software/herdr.go`
- Modify: `internal/software/catalog.go` (`DefaultCatalog`, `List` order)
- Modify: `internal/software/catalog_test.go`

**Interfaces:**
- Produces: `func herdrPackage() Package`
- Digests (no `sha256:` prefix), from GitHub release `v0.7.4`:
  - `herdr-linux-x86_64` → `bc0fc02d4ba500f9cac2353a43e67fe036785ecca6eb55378e050fac3c103059`
  - `herdr-linux-aarch64` → `544e0002de42806d1ab64ccdef3a7e7414f24717b0b6b022bc9e57d2eefd26a2`

- [ ] **Step 1: Write failing catalog test**

```go
func TestDefaultCatalogIncludesHerdr(t *testing.T) {
	t.Parallel()
	pkg, ok := software.DefaultCatalog().Get("herdr")
	if !ok {
		t.Fatal("missing herdr")
	}
	if err := pkg.Validate(); err != nil {
		t.Fatal(err)
	}
	if pkg.ID != "herdr" || len(pkg.Verify) == 0 {
		t.Fatalf("%+v", pkg)
	}
	pin := pkg.Pins[0]
	if pin.Manager != "github-release" || pin.Name != "ogulcancelik/herdr" || pin.Version != "0.7.4" {
		t.Fatalf("pin=%+v", pin)
	}
	want := map[string]string{
		"x86_64":  "bc0fc02d4ba500f9cac2353a43e67fe036785ecca6eb55378e050fac3c103059",
		"aarch64": "544e0002de42806d1ab64ccdef3a7e7414f24717b0b6b022bc9e57d2eefd26a2",
	}
	for arch, sum := range want {
		found := false
		for _, a := range pin.Assets {
			if a.Arch == arch && a.SHA256 == sum {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %s digest", arch)
		}
	}
}
```

- [ ] **Step 2: Run test — expect fail**

Run: `go test ./internal/software/ -count=1 -run TestDefaultCatalogIncludesHerdr`

Expected: FAIL `missing herdr`

- [ ] **Step 3: Implement `herdrPackage` and register**

```go
func herdrPackage() Package {
	pkg := Package{
		ID:          "herdr",
		Name:        "Herdr",
		Description: "Agent-aware terminal multiplexer. Run herdr from the instance terminal.",
		Pins: []Pin{{
			Manager: "github-release",
			Name:    "ogulcancelik/herdr",
			Version: "0.7.4",
			Assets: []ReleaseAsset{
				{Arch: "x86_64", Filename: "herdr-linux-x86_64", SHA256: "bc0fc02d4ba500f9cac2353a43e67fe036785ecca6eb55378e050fac3c103059"},
				{Arch: "aarch64", Filename: "herdr-linux-aarch64", SHA256: "544e0002de42806d1ab64ccdef3a7e7414f24717b0b6b022bc9e57d2eefd26a2"},
			},
		}},
		Verify: [][]string{{"herdr", "--version"}},
	}
	if err := pkg.Validate(); err != nil {
		panic(err)
	}
	return pkg
}
```

Update `DefaultCatalog` to include both packages. Update `List` order to `[]string{"pi", "herdr"}`.

- [ ] **Step 4: Run tests — expect pass**

Run: `go test ./internal/software/ -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/software/herdr.go internal/software/catalog.go internal/software/catalog_test.go
git commit -m "$(cat <<'EOF'
feat: add herdr package to software catalog

EOF
)"
```

---

### Task 4: Wire architecture + version into instance install

**Files:**
- Modify: `internal/app/instances/service.go` (`installPackage`)
- Modify: `internal/app/instances/service_test.go`
- Modify: any other `software.Install(` call sites (grep)

**Interfaces:**
- Consumes: `software.Install(..., InstallOptions{Architecture})`
- Architecture source: `s.runtime.DiscoverCapabilities(ctx).Architecture` (guest matches host for managed images). If empty after discover, return install error.
- Version recorded on success: first `github-release` pin version, else existing npm pin version logic.

- [ ] **Step 1: Write failing service test**

Extend fake runtime if needed so `Exec` accepts stdin. Add:

```go
func TestInstallSoftwareHerdrRecordsVersion(t *testing.T) {
	// create running VPS; stub capabilities Architecture x86_64
	// stub release fetch by injecting software test hook OR make Service accept fetcher
	// Prefer: software.SetReleaseFetcherForTest in test, or InstallOptions via service field for tests
	row, err := service.InstallSoftware(ctx, owner, id, "herdr")
	// assert row.Status installed, row.Version == "0.7.4"
}
```

Practical seam (pick one and use consistently):

```go
// in software package
var defaultReleaseFetcher ReleaseFetcher = httpReleaseFetcher{...}

func SetReleaseFetcherForTest(f ReleaseFetcher) func() {
	prev := defaultReleaseFetcher
	defaultReleaseFetcher = f
	return func() { defaultReleaseFetcher = prev }
}
```

In the test, set fetcher to return bytes matching the pinned x86_64 digest (compute sha256 of a small payload and temporarily is wrong — instead hash known bytes and **override only in unit tests of software**; for service test, use a fetcher that returns bytes whose sha256 equals the real pin by reading `herdr` package pin and generating matching content via a test helper that cannot forge GitHub — simplest approach: in service test, call `software.SetReleaseFetcherForTest` with a fetcher that returns `[]byte("x")` **after** swapping the catalog — too heavy.

Simpler service-test approach: keep service test focused on options wiring with a **fake catalog install hook** only if already present; otherwise test architecture plumbing with a package-local stub:

Add optional `s.releaseFetcher software.ReleaseFetcher` on Service for tests; production nil.

For digest: test fetcher returns content `[]byte("herdr-test")` and use `software` test that already covers digest; service test can use `InstallSoftware` with fetcher that returns bytes matching pin by computing:

```go
pkg, _ := software.DefaultCatalog().Get("herdr")
// Cannot change pin. Instead fetch real digest from pin and use a custom test-only package via InstallSoftware path that only checks Architecture is passed — mock runtime Exec success and mock fetcher returning bytes with matching sha256:

body := []byte("herdr-test-binary")
sum := sha256.Sum256(body)
// PROBLEM: pin digest won't match.

```

**Resolve:** Service test does not re-verify GitHub digests. Add `software.Install` dependency injection on Service:

```go
type softwareInstaller func(ctx context.Context, execer software.GuestExecer, ref string, pkg software.Package, opts software.InstallOptions) error
// s.installSoftwareFn defaults to software.Install
```

Test asserts `opts.Architecture == "x86_64"` and stub installer returns nil; then assert version `0.7.4` from pin.

- [ ] **Step 2: Run test — expect fail**

Run: `go test ./internal/app/instances/ -count=1 -run TestInstallSoftwareHerdr`

Expected: FAIL

- [ ] **Step 3: Implement wiring**

In `installPackage`:

```go
caps, err := s.runtime.DiscoverCapabilities(ctx)
// handle err
arch := caps.Architecture
installFn := s.installSoftwareFn
if installFn == nil {
	installFn = software.Install
}
if err := installFn(ctx, s.runtime, instance.RuntimeRef, pkg, software.InstallOptions{Architecture: arch}); err != nil {
	// existing failed path
}
version := ""
for _, pin := range pkg.Pins {
	if pin.Manager == "github-release" {
		version = pin.Version
		break
	}
}
if version == "" {
	for _, pin := range pkg.Pins {
		if pin.Manager == "npm" {
			version = pin.Version
			break
		}
	}
}
```

Update all `software.Install(` call sites to the new signature.

- [ ] **Step 4: Run tests — expect pass**

Run: `go test ./internal/app/instances/ ./internal/software/ -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/instances/service.go internal/app/instances/service_test.go internal/software/
git commit -m "$(cat <<'EOF'
feat: pass guest architecture into software install for herdr

EOF
)"
```

---

### Task 5: API catalog asserts `herdr`

**Files:**
- Modify: `internal/httpapi/handler_test.go` (`TestSoftwareCatalogAndInstall`)

**Interfaces:**
- Consumes: `DefaultCatalog()` including `herdr` (no OpenAPI schema change — catalog is free-form items)

- [ ] **Step 1: Extend catalog handler test**

```go
// after GET /v1/software
var body generated.ListSoftwareResponse
// decode
ids := map[string]bool{}
for _, item := range body.Items {
	ids[item.Id] = true
}
if !ids["pi"] || !ids["herdr"] {
	t.Fatalf("catalog ids=%v", ids)
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/httpapi/ -count=1 -run TestSoftwareCatalogAndInstall`

Expected: PASS (or FAIL until Task 3 merged — run after Task 3)

- [ ] **Step 3: Commit**

```bash
git add internal/httpapi/handler_test.go
git commit -m "$(cat <<'EOF'
test: assert software catalog lists herdr

EOF
)"
```

---

### Task 6: Operator / developer docs

**Files:**
- Modify: `docs/operators/pi-profile-and-launcher.md` (or add `docs/operators/software-catalog.md` if missing)
- Create or modify: `docs/development/` note for Herdr pins
- Prefer a short `docs/operators/software-catalog.md` covering Pi + Herdr if operator docs still say Devbox-only

**Content requirements:**
- Herdr is catalog package id `herdr`, pin `ogulcancelik/herdr@0.7.4`
- Install at create or Software install API; run `herdr` in Terminal
- Optional: with Pi installed, `herdr integration install pi`
- Pin bump: update digests in `internal/software/herdr.go` from GitHub release asset digests; never `curl|sh`
- Non-goals: no Launch Herdr, no managed `herdr update`

- [ ] **Step 1: Write docs**

- [ ] **Step 2: Commit**

```bash
git add docs/
git commit -m "$(cat <<'EOF'
docs: document Herdr software catalog package

EOF
)"
```

---

## Spec coverage check

| Spec item | Task |
|---|---|
| Catalog package `herdr` | 3 |
| `github-release` pin manager + validation | 1 |
| Host fetch + sha256 + fail closed | 2 |
| Arch-specific Linux assets | 2, 3 |
| Guest tee/chmod/mv + verify | 2 |
| Create/install/API surfaces | 3, 4, 5 (existing endpoints) |
| Version on installed row | 4 |
| Docs + optional Pi integration note | 6 |
| No Launch Herdr / no tmux replace / no uninstall | Global constraints |
| UI checkbox/Software panel | Deferred to parent catalog UI if not present; API-complete without new endpoints |

## Placeholder scan

None intentional. Digests and URLs are concrete. Service test seam (`installSoftwareFn`) is explicit.

## UI note

If the console Software panel / create checkboxes from the VPS catalog plan are not landed yet, this plan still ships a complete control-plane package (catalog + install). When that UI lands, `herdr` appears automatically from `GET /v1/software` — no extra OpenAPI fields. Do not block Herdr backend on unfinished Launch Pi removal UI work.
