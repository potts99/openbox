# Instance Metrics Monitoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Always-on Incus usage sampling into 60-minute in-memory rings, pushed over WebSocket to a minimal monitoring strip on the instance-by-id page.

**Architecture:** Sampler periodic in `openboxd` → `MetricsHub` rings/pubsub → `GET /v1/instances/{id}/metrics` WebSocket (terminal auth parity) → `InstanceMetrics` UI with live readouts + SVG sparklines.

**Tech Stack:** Go (`openboxd`, Incus HTTP API, `coder/websocket`), React/TypeScript dashboard, existing console CSS.

**Spec:** `docs/superpowers/specs/2026-07-15-instance-metrics-monitoring-design.md`

## Global Constraints

- Client never supplies Incus identity; resolve `RuntimeRef` from owned OpenBox instance.
- Metrics storage is memory-only; no SQLite metrics schema.
- Sample interval default 10s; ring ≈ 360 points (60 minutes).
- WebSocket CSRF/origin rules match browser terminal.
- No new chart libraries; SVG sparklines only.
- YAGNI: no alerting, fleet views, or persistence across restart.

---

## File structure

| Path | Responsibility |
|------|----------------|
| `internal/runtime/runtime.go` | `InstanceUsageReader` + `UsageSnapshot` types |
| `internal/runtime/incus/usage.go` | Decode Incus `/state` → usage |
| `internal/runtime/fake/fake.go` | Fixture usage for tests |
| `internal/app/metrics/hub.go` | Ring + pub/sub |
| `internal/app/metrics/sampler.go` | Periodic sample + delta rates |
| `internal/app/metrics/*_test.go` | Hub/sampler unit tests |
| `internal/httpapi/metrics_handlers.go` | WS upgrade + frames |
| `internal/httpapi/handler.go` / `daemon.go` | Route + wire-up |
| `web/src/metrics/session.ts` | WS URL + frame parse |
| `web/src/components/InstanceMetrics.tsx` | Monitoring UI |
| `web/src/pages/InstancePage.tsx` | Mount section |
| `web/src/styles.css` | Minimal monitoring styles |

---

### Task 1: Runtime usage reader (Incus decode)

**Files:**
- Create: `internal/runtime/incus/usage.go`, `internal/runtime/incus/usage_test.go`
- Modify: `internal/runtime/runtime.go`, `internal/runtime/fake/fake.go`

- [ ] Add `UsageSnapshot` (CPU nanos, memory/disk bytes, net rx/tx counters, status) and `InstanceUsageReader` interface beside `Runtime`.
- [ ] Write failing tests decoding fixture Incus `/state` JSON (cpu/memory/disk/network).
- [ ] Implement `Adapter.InstanceUsage` expanding state decode (keep address helpers working).
- [ ] Fake runtime returns configurable usage by ref.
- [ ] `go test ./internal/runtime/...` passes.
- [ ] Commit: `feat: decode Incus instance usage from /state`

### Task 2: Metrics hub + sampler

**Files:**
- Create: `internal/app/metrics/hub.go`, `sampler.go`, types, tests

- [ ] Ring buffer: append, capacity eviction, `Snapshot()` oldest→newest.
- [ ] Hub: `Publish`, `Subscribe` (buffered; drop/slow-disconnect policy), `Remove` instance.
- [ ] Sampler: list running instances (inject list + usage funcs), compute CPU% and net bps from previous raw counters, publish `Sample`.
- [ ] Unit tests for ring, deltas, gap reset, subscribe delivery.
- [ ] `go test ./internal/app/metrics/...` passes.
- [ ] Commit: `feat: add in-memory metrics hub and sampler`

### Task 3: WebSocket metrics handler

**Files:**
- Create: `internal/httpapi/metrics_handlers.go`, `metrics_handlers_test.go`
- Modify: `internal/httpapi/handler.go`

- [ ] Route `GET …/metrics` like terminal (auth already applied).
- [ ] CSRF/origin parity with terminal helpers.
- [ ] On accept: authorize owned instance → send `snapshot` → forward hub samples as `sample` frames.
- [ ] Tests: forbidden/foreign id; happy path snapshot+sample with fake hub.
- [ ] `go test ./internal/httpapi/...` passes focused metrics + existing terminal auth tests.
- [ ] Commit: `feat: stream instance metrics over WebSocket`

### Task 4: Wire sampler into openboxd

**Files:**
- Modify: `cmd/openboxd/daemon.go` (and config/flags if pattern exists)

- [ ] Construct hub + sampler; `periodic` every 10s.
- [ ] Drop hub series when instances are deleted if a clean hook exists; otherwise lazy eviction on missing refs is acceptable for v1.
- [ ] Smoke: daemon builds (`go build ./cmd/openboxd`).
- [ ] Commit: `feat: sample running instance metrics in openboxd`

### Task 5: Frontend monitoring strip

**Files:**
- Create: `web/src/metrics/session.ts`, `web/src/components/InstanceMetrics.tsx` (+ tests)
- Modify: `web/src/pages/InstancePage.tsx`, `web/src/styles.css`, `web/src/api/client.ts` if CSRF helper needed

- [ ] WS connect with csrf query; handle `snapshot` / `sample` / `error`.
- [ ] Four live readouts + four SVG sparklines; connection status.
- [ ] Mount on `InstancePage` above Detail.
- [ ] Component tests with mocked WS or injected series.
- [ ] `npm test` (or project equivalent) for touched files.
- [ ] Commit: `feat: show instance metrics monitoring on detail page`

### Task 6: Docs polish

**Files:**
- Create: `docs/development/instance-metrics.md` (short; mirror terminal notes)
- Optional: note in `docs/api/v1.md`

- [ ] Document endpoint, frames, sampling defaults, restart behavior.
- [ ] Commit: `docs: describe instance metrics WebSocket`

---

## Verification

```sh
go test ./internal/runtime/... ./internal/app/metrics/... ./internal/httpapi/... ./cmd/openboxd -count=1
cd web && npm test -- --run
```

Manual: run daemon, open a running instance page, confirm live values and sparkline growth; refresh within 60m and confirm history remains.
