# HTTPS routes — development notes

Slice 10 packages:

| Package / path | Role |
|---|---|
| `internal/routes` | Policy, CRUD service, certificate allow, gateway access, DNS validation, route tokens |
| `internal/caddy` | Generate Caddyfile from approved routes; atomic apply/rollback |
| `internal/httpapi` | `/v1/routes`, `/v1/certificates/allow`, `/v1/gateway/auth` |
| `internal/sshgateway` | Direct-TCPIP for `openbox forward` (instance loopback only) |
| `cmd/openbox/route.go` | `route add\|ls\|rm\|publish` |
| `cmd/openbox/forward.go` | SSH local-forward helper |
| `deploy/caddy/` | Operator base Caddyfile + generated include |

## Safety invariants

- Upstream targets come from managed instance identity (`RuntimeRef`), never
  client-supplied IPs (SSRF posture).
- New routes are always private; publish is explicit.
- Certificate ask allows only persisted route hostnames.
- Private routes require owner principal or `obr_` route token.
- Caddy `reverse_proxy` emits Host / X-Forwarded-* and `flush_interval -1` for
  WebSockets/SSE/streaming.
- Gateway apply failure rolls back config and does not stop instances; OpenBox
  SSH defaults to `:2222`, not host port 22.

## Tests to run

```sh
go test ./internal/routes/ ./internal/caddy/ ./internal/httpapi/ ./internal/sshgateway/ ./cmd/openbox/
```
