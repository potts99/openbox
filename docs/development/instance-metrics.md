# Instance metrics development notes

The instance monitoring strip keeps these boundaries separate:

- `internal/runtime` (Incus adapter) decodes cumulative counters from
  `GET /1.0/instances/{ref}/state`.
- `internal/app/metrics` owns the always-on sampler, 60-minute in-memory rings,
  and pub/sub hub. It never speaks HTTP.
- `internal/httpapi` owns authorization, origin/CSRF upgrade policy, and the
  WebSocket frame protocol.
- The dashboard (`web/src/metrics/`, `InstanceMetrics`) is a thin client of the
  stream and must not invent alternate auth or Incus identities.

## WebSocket path

```text
GET /v1/instances/{openbox_instance_id}/metrics
```

After session/cookie (or bearer) auth, the handler loads the owned instance and
subscribes to that OpenBox instance ID in the hub. A client cannot address an
arbitrary Incus instance through the path.

## CSRF and origin

Same rules as the browser terminal: cookie upgrades require matching `Origin`
and may pass CSRF as `?csrf=` on the WebSocket handshake. Bearer clients may
omit `Origin`.

## Frames

Server → client JSON text frames:

- `snapshot` — `limits`, `interval_seconds`, `samples[]` (oldest → newest)
- `sample` — one derived point (`cpu_percent`, `memory_bytes`, `disk_bytes`,
  `net_rx_bps`, `net_tx_bps`)
- `error` — fatal stream error

Rate fields may be omitted on the first sample after a gap when deltas cannot
be computed. History is memory-only and clears on `openboxd` restart.

## Sampling

`openboxd` runs a periodic sampler (`--metrics-interval`, default 10s) over
instances with `observed_state=running` and a non-empty `RuntimeRef`. Ring
capacity covers approximately 60 minutes at the configured interval.

## Focused checks

```sh
go test ./internal/runtime/... ./internal/app/metrics/... ./internal/httpapi/... ./cmd/openboxd -count=1
cd web && pnpm test -- src/metrics src/components/InstanceMetrics.test.tsx src/pages/InstancePage.test.tsx
```
