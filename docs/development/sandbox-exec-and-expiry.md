# Sandbox exec and expiry (developers)

Slice 13 packages:

| Package | Responsibility |
|---|---|
| `internal/sandbox` | Create defaults, exec validation, Run framing, expiry sweeper, exec gates/rate limits, status labels |
| `internal/execstream` | NDJSON frame codec for stdout/stderr/exit/error |
| `internal/app/instances` | `MarkExpired`, `ExtendExpiry`, `Exec` with concurrency + rate limits |
| `internal/httpapi` | `/exec` NDJSON stream and `/extend` handlers |
| `cmd/openbox` | `sandbox exec` / `sandbox extend`, richer `inspect` |
| `web/src/pages/Sandbox.tsx` | Countdown / egress / cleanup failure panel |

## Testing focus

```sh
go test ./internal/sandbox/ ./internal/execstream/ ./internal/app/instances/ \
  ./internal/httpapi/ ./internal/persistence/sqlite/ ./cmd/openbox/
cd web && pnpm test
```

Key gates:

- Fake-clock expiry selection and idempotent `MarkExpired`
- Extension rejected after desired `deleted` / observed `deleting`
- Exec framing, cancel, timeout, chunking, busy + rate-limited sinks
- HTTP NDJSON stream does not persist output
