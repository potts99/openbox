# ADR 0010: Versioned API and asynchronous command boundary

- Status: Accepted
- Date: 2026-07-15

## Context

OpenBox needs one contract for automation and the native CLI. Lifecycle work can
outlive an HTTP connection, and retrying a request after a lost response must
not repeat an Incus mutation. Authentication arrives in a later slice, so an
early API must not become an unauthenticated public control plane.

## Decision

`api/openapi.yaml` is the canonical v1 HTTP contract. Generated transport types
remain inside `internal/httpapi/generated`; domain and application packages do
not import them. Clients request `v1` with `X-OpenBox-API-Version`, and servers
reject incompatible versions explicitly.

Mutation handlers validate an `Idempotency-Key` and submit durable commands.
Submission transactionally records the desired state and pending operation,
then returns without performing an Incus mutation. The existing fenced local
worker executes the operation independently of the HTTP request context.
Repeating the same key and request returns the original operation; reusing a
key for different work is a conflict.

Operation events are persisted in sequence order. The SSE endpoint replays
events after `Last-Event-ID`, follows new events with bounded polling and
heartbeats, and closes after a terminal event. Disconnecting a stream cancels
only the stream.

Cancellation is deliberately narrow in v1. Only an unclaimed pending operation
at the initial `runtime` stage is safe to cancel. Cancellation and worker claim
compete in one SQLite transaction. A successful cancellation rolls back the
pending desired-state change or removes an unstarted create placeholder; after
a claim or stage advance, cancellation fails closed.

Until owner authentication is implemented, the daemon selects one configured
local owner and defaults to a loopback listener. Owner identity is never
accepted from an API request body or header.

## Consequences

CLI commands and future UI code share one stable transport without direct
SQLite or Incus access. HTTP timeouts and disconnects do not abandon durable
work. Operation polling and SSE can resume after reconnect. Public binding must
not be enabled before the authentication slice, and cancellation cannot be
used as a general rollback mechanism once external work may have begun.
