# Control-plane hardening and management surface

## Goal

Ship a backward-compatible OpenBox release that repairs the identified
upgrade, routing, authentication, operation, quality, and management-surface
defects; make the resulting build safe to deploy to the production test host.

## Scope

The work covers the confirmed defects from the implementation review plus the
currently missing owner-management surfaces: instance creation and lifecycle
management, expiry and protection, software operations, routes and route
tokens, SSH keys, snapshots, cloning, and dashboard operation visibility.
Existing v1 API and CLI behavior remains compatible. New behavior is additive.

## Persistence and upgrades

Migration 009 must not rebuild `instances` while foreign keys are enabled.
Replace its unsafe schema-rewrite behavior with a forward-only migration that
creates software state and leaves existing `devbox` records intact. New
instance creation continues to reject unsupported kinds. Add upgrade tests
that start from a populated pre-009 database containing instances, routes,
and snapshots.

Route hostnames are normalized to lowercase DNS names at the domain boundary.
Persistence enforces global case-insensitive uniqueness so certificate and
gateway lookup cannot select an arbitrary duplicate.

## Proxy and authentication boundary

The daemon gains explicit trusted-proxy configuration. Only direct peers in
that allowlist may contribute forwarded client IP and HTTPS protocol values.
Those validated values drive login rate-limit keys, cookie `Secure`, and HSTS.
Direct loopback operation remains safe without proxy headers. Session lookup is
read-only; rotation is not performed as a side effect of `GET /v1/session`.

## Durable operations

All package installations are represented by durable operations with
idempotency and progress events. A create operation completes only after its
requested packages complete. Installation failure leaves a failed/retryable
operation and accurately records failed software state.

Mutation serialization moves from a single global gate to per-instance gates,
allowing unrelated instance operations to use configured worker concurrency.

## HTTPS routes

The daemon owns a route synchronizer. For each route mutation it validates
intent, resolves managed upstreams, writes/validates/reloads Caddy atomically,
and commits the corresponding route database change only when the gateway
change succeeds. At startup it rebuilds the generated Caddy include from
persisted routes.

Daemon configuration supplies Caddy paths/reload invocation and expected
public IPs. DNS validation uses the host resolver and these configured IPs.
Route tokens receive additive v1 endpoints and CLI commands for create, list,
and revoke.

## Management surfaces

Add compatible v1 and CLI coverage for protection, expiry extension, SSH keys,
snapshots, clone/copy, routes, and route tokens. The dashboard receives owner
flows for creation, lifecycle/deletion/protection/expiry, routes and tokens,
SSH keys, snapshots and cloning, and software-operation state. Operations are
refreshed while visible. Terminal-heavy code is loaded lazily where possible.

## Validation and release

Add focused regression tests for each fixed defect, API/CLI contract tests,
and dashboard tests. The full quality gate must pass: formatting, lint,
tests, generated assets, license headers, production build, and race tests for
the concurrent subsystems. Deploy only from a clean checkout, then verify the
production test daemon, authenticated API, dashboard, and route lifecycle.
