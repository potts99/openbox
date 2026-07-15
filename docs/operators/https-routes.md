# HTTPS routes and optional domains

OpenBox can expose instance ports over HTTPS through a separate Caddy process.
Owning a domain is optional: without one, use `openbox forward` (SSH tunnel) or
the private API/dashboard over loopback.

## Concepts

A **route** binds one managed instance port to a hostname with visibility:

| Visibility | Access |
|---|---|
| `private` (default) | Owner session, owner API bearer, or route-scoped `obr_…` token via Caddy `forward_auth` |
| `public` | No OpenBox login (still only the approved upstream) |

Routes never auto-publish listening ports. Create explicitly, then `publish` if
you want public visibility.

`tls_state` tracks custom-domain readiness after DNS checks:

| State | Meaning |
|---|---|
| `none` | Newly created; DNS not checked |
| `pending` | No usable DNS answer, or expected host IPs not configured |
| `invalid` | Hostname resolves, but not to an expected OpenBox host address |
| `active` | Hostname resolves to an expected host address |

## CLI

```sh
openbox route add my-vm --port 3000 --hostname app.example.com
openbox route ls
openbox route publish ROUTE_ID
openbox route rm ROUTE_ID

# Without a public domain — SSH local forward through the gateway:
openbox forward --host box.example my-vm 3000
openbox forward --host box.example --local 13000 --print my-vm 3000
```

`forward` uses `ssh -N -L` to `INSTANCE@HOST` on port 2222 by default. The
gateway only allows Direct-TCPIP to the instance loopback mapping (SSRF-safe).

## Caddy

See [deploy/caddy/README.md](../../deploy/caddy/README.md) for layout, on-demand
TLS ask (`/v1/certificates/allow`), `forward_auth` (`/v1/gateway/auth`), atomic
apply/rollback, and failure independence from instance lifecycle and host SSH.

## API

Documented in [API v1](../api/v1.md): route CRUD, publish, validate-dns,
suggested-ports, certificate allow, and gateway auth.
