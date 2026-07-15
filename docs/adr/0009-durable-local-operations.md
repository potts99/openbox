# ADR 0009: Durable local operations and conservative reconciliation

- Status: Accepted
- Date: 2026-07-15

## Context

Lifecycle calls cross a process boundary into Incus. The daemon can stop after Incus accepts an action but before SQLite records the result, and Incus can be temporarily unavailable. OpenBox must recover without duplicating instances or replacing lost persistent disks with empty ones.

## Decision

SQLite remains the source of truth for local operation status. A bounded in-process worker claims pending or abandoned operations with expiring leases. Every claim receives a unique fencing token, including successive claims made by the same worker identity. While executing, the worker renews its lease periodically; losing ownership or failing to renew cancels the attempt. Worker-originated stage, instance-state, retry, failure, and completion writes must present the current token. After a replacement claim is issued, SQLite rejects every durable write from the stale token.

Attempts, retry deadlines, durable stages, and structured events are recorded transactionally. Transient failures use bounded exponential backoff; correctable and integrity-threatening failures become terminal. Synchronous lifecycle callers do not require a worker claim, while recovery executed by the worker carries its claim through the lifecycle service. The design is deliberately single-node and does not introduce a broker, distributed lock, or leader election.

Lifecycle stages bracket external actions. Recovery first inspects the stable runtime reference and verifies its OpenBox instance identity and isolation. A create may be repeated only while its durable stage is still before a confirmed runtime creation. Once `container_created` or `vm_created` has been recorded, absence is `runtime_missing`; recovery and reconciliation never create a blank replacement.

Reconciliation compares non-tombstoned SQLite instances with one runtime inventory. It converges running, stopped, and deleted desired states through the same identity-safe lifecycle service. Unknown runtime resources and replacement identities are diagnostic-only and are left untouched. If runtime inventory is unavailable, OpenBox enters degraded read-only mode while SQLite metadata remains readable.

Recovery actions are explicit. `forget` uses verified deletion/tombstoning, `adopt` requires an existing matching OpenBox identity, and `restore` requires an operator-selected restorer plus an existing `runtime_missing` record. Restore selection is never inferred.

## Consequences

Restarting the daemon can reclaim expired local work and expose every retry and terminal failure through one event model. Lease renewal prevents a healthy long-running attempt from being reclaimed merely because it exceeds the initial lease. If takeover still occurs after process failure, loss of connectivity, or delayed cancellation, the replacement token fences the stale attempt from changing durable state. Runtime actions remain context-cancellable, idempotent, identity-checked, and bracketed by fenced stages so recovery can inspect ambiguous external outcomes safely. The local lease-and-token model must be replaced before multi-node active/active execution is supported.
