# Instance metrics monitoring — design

**Status:** Approved for implementation (Approach 2: in-memory rings + WebSocket push)  
**Date:** 2026-07-15  
**Surface:** Instance-by-id page (`InstancePage`) + `openboxd` sampler

## Goal

Show live CPU, memory, disk, and network usage for an instance in a neat, minimal layout on the instance detail screen, with basic history charts backed by a ~60-minute daemon-retained window.

## Non-goals

- Alerting, thresholds, or anomaly detection
- Multi-instance fleet dashboards
- Persisting history across `openboxd` restart
- Guest-agent metrics beyond what Incus `/state` already exposes
- Sub-second sampling or long-term retention (hours/days)

## Decisions

| Topic | Choice |
|-------|--------|
| Metrics | CPU %, memory used, disk used, network rx/tx rates |
| History | ~60 minutes, in-memory ring buffer |
| Sampling | Always-on for running instances (~10s interval) |
| Transport | WebSocket push (mirror terminal auth/CSRF/origin) |
| Persistence | None (rings cleared on daemon restart) |
| UI | Compact monitoring strip on instance-by-id page with sparklines |

## Architecture

```text
Incus GET /1.0/instances/{ref}/state
        │
        ▼
┌───────────────┐     append      ┌──────────────────┐
│ Sampler       │ ──────────────► │ MetricsHub       │
│ (periodic 10s)│                 │ rings + pub/sub  │
└───────────────┘                 └────────┬─────────┘
                                           │ snapshot + sample frames
                                           ▼
                              WS GET /v1/instances/{id}/metrics
                                           │
                                           ▼
                              InstancePage monitoring section
```

### Sampler

- New `periodic()` goroutine in `openboxd` (same pattern as reconciliation; does **not** mutate instance state).
- Each tick: list durable instances with `ObservedRunning` and non-empty `RuntimeRef`; for each, call runtime `InstanceUsage(ctx, ref)`.
- Derive rates that need deltas (CPU %, network B/s) from the previous raw counters for that instance.
- Skip / leave gaps when Incus returns not-found or non-running; keep existing ring until eviction or instance delete.
- Default interval: **10 seconds**. Tunable via daemon flag if already patterned for other intervals; otherwise a constant is fine for v1.

### Metrics hub

- Owns per-instance fixed-size rings (~360 points ≈ 60 min @ 10s).
- Thread-safe subscribe/unsubscribe by OpenBox instance ID.
- On append: notify subscribers with the new sample.
- On instance delete (or tombstone): drop ring and close subscribers.
- Memory-only; no SQLite schema.

### Runtime boundary

- Add a narrow capability on the Incus adapter, e.g. `InstanceUsage(ctx, ref) (UsageSnapshot, error)`, decoding Incus `/state` fields for CPU nanoseconds, memory usage, disk usage (root), and per-nic byte counters (aggregate non-`lo`).
- Prefer a small interface (`InstanceUsageReader`) so the fake runtime can supply fixtures without widening the whole `Runtime` surface unnecessarily.
- Handlers never accept a client-supplied Incus identity; they resolve `RuntimeRef` from the owned OpenBox instance row (same as terminal).

## API and protocol

### Endpoint

```text
GET /v1/instances/{instance_id}/metrics
```

WebSocket upgrade. Auth, CSRF (`?csrf=` on cookie upgrades), and Origin rules match the browser terminal (`docs/development/browser-terminal.md`).

Like terminal, the upgrade path may live as a handler-only route. Prefer documenting a companion REST snapshot in OpenAPI only if useful for CLI/tests; the live UI uses WebSocket.

### Frame protocol (JSON text frames)

Server → client:

| Type | When | Payload |
|------|------|---------|
| `snapshot` | Immediately after accept | `limits` (vcpus, memory_bytes, disk_bytes), `interval_seconds`, `samples[]` (ring contents oldest→newest) |
| `sample` | Each new point | One sample object |
| `error` | Fatal stream error | `code`, `message` then close |

Sample object:

```json
{
  "t": "2026-07-15T20:00:00Z",
  "cpu_percent": 12.5,
  "memory_bytes": 2147483648,
  "disk_bytes": 8589934592,
  "net_rx_bps": 10240,
  "net_tx_bps": 4096
}
```

- Omit or null rate fields on the first sample after a gap when a delta cannot be computed.
- CPU percent is normalized against the instance’s vCPU limit (and clamped sensibly).
- Memory/disk are absolute bytes used; UI compares to limits from the snapshot / instance detail.
- Network is aggregate bytes/sec across non-loopback nics.

Client → server: none required for v1 (read-only stream). Idle timeout may close quiet connections; client reconnects and receives a fresh snapshot.

### Authorization

1. Authenticate session/bearer.
2. Load owned instance by `{instance_id}`.
3. Upgrade only if authorized.
4. Subscribe hub for that OpenBox ID (not Incus ref).

Stopped instances: still allow connect; send snapshot of whatever ring remains (may be empty/stale) and no further samples until running again.

## UI (instance-by-id)

Place a **Monitoring** section on `InstancePage` above the existing Detail block when the page is ready (show for any observed state; charts empty/stale when stopped).

Minimal layout:

1. **Row of four live readouts** — CPU %, Memory (used / limit), Disk (used / limit), Net (↓ / ↑ human rates). Quiet typography consistent with `detail-grid` / ledger headers; no card grid clutter.
2. **Four sparklines** — one per series over the retained window. SVG, no chart library. Shared time axis implied by sample order; no dense tick labels—just “last 60 min” caption and current values.
3. **Connection state** — subtle status: connecting / live / reconnecting / unavailable. Do not block the rest of the page.

Behavior:

- Connect WebSocket on mount; disconnect on unmount.
- Apply `snapshot` to seed charts; append `sample` points; trim to window client-side as a safety bound.
- If WS fails, show unavailable; optional light retry with backoff (reuse terminal reconnect spirit, simpler).

Visual tone: match existing console (ledger header, state pill language). Monitoring should read as one calm strip, not a dashboard of cards.

## Error handling

| Case | Behavior |
|------|----------|
| Unauthenticated / forbidden | HTTP 401/403 before upgrade |
| Unknown instance | HTTP 404 before upgrade |
| Incus usage fetch fails for one instance | Log at debug/warn; skip that tick; do not poison other instances |
| First sample / counter reset | Rate fields absent; absolute fields still published when available |
| Hub full / slow consumer | Drop samples for that subscriber or disconnect slow client; sampler must never block on WS writes |
| Daemon restart | Empty rings until samples accumulate again |

## Testing

- **Incus decode:** unit tests for `InstanceUsage` mapping from fixture `/state` JSON (CPU/memory/disk/network).
- **Ring + hub:** append, eviction at capacity, subscribe gets snapshot semantics, unsubscribe stops delivery.
- **Sampler deltas:** two counters → expected CPU % and bps; gap resets rates.
- **HTTP/WS:** auth/CSRF/origin parity with terminal; owned vs foreign instance; snapshot then sample delivery with fake hub.
- **UI:** component/page test that seeded snapshot renders readouts (and empty/unavailable states).

## Files (expected)

**Create:** `internal/app/metrics/…`, `internal/runtime/incus/usage.go` (+ tests), `internal/httpapi/metrics_handlers.go` (+ tests), `web/src/metrics/…`, `web/src/components/InstanceMetrics.tsx` (+ tests), this spec + implementation plan under `docs/superpowers/`.

**Modify:** `cmd/openboxd/daemon.go` (wire sampler), `internal/httpapi/handler.go` (route + deps), `internal/runtime/runtime.go` and `fake`, `web/src/pages/InstancePage.tsx`, `web/src/styles.css`, `web/src/api/client.ts` as needed for CSRF/WS URL helpers.

## Success criteria

- Opening a running instance shows live CPU/memory/disk/net with updating sparklines.
- Leaving the page and returning within ~60 minutes still shows history collected while away.
- No Incus identity ever accepted from the client path.
- Restarting `openboxd` clears history; UI recovers cleanly as samples resume.
