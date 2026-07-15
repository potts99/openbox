# ADR 0007: Container lifecycle identity and safety

- Status: Accepted
- Date: 2026-07-15

## Context

OpenBox needs its first complete runtime path without coupling callers to Incus names or allowing a retry to delete an unrelated replacement resource.

## Decision

Application services own create, inspect, start, stop, restart, refresh, and delete. They call a narrow container lifecycle interface; callers never invoke Incus directly.

Every container receives a stable runtime reference derived from its immutable OpenBox instance ID, not its human-readable name. OpenBox stores that reference separately and applies managed, resource, owner, and instance identity labels. Every destructive operation inspects and verifies those labels first. A runtime resource with missing or different identity labels is never adopted or deleted.

Image aliases are resolved to fingerprints before creation. The dedicated Incus project sets `features.images=false`, making default-project images and aliases visible without duplicating their storage. Owner-key injection requires an image whose Incus properties contain `variant=cloud`; administrators can explicitly mark a custom compatible image with `user.openbox.cloud_init=true`. OpenBox emits recognizable `#cloud-config` user data and rejects an incompatible image before creating an instance.

Slice 03 creates only unprivileged system containers and records actual isolation as `container`. CPU, memory, disk, the owner's SSH key, and structured OpenBox metadata cross the runtime boundary as typed data. The Incus adapter uses its local Unix-socket HTTP API and structured JSON; it never invokes a command-line client or constructs shell commands. Short request and dial timeouts are separate from a bounded two-minute asynchronous-operation wait, so normal graceful stops are not cut off by the ten-second request default.

Deletion is complete only after a follow-up inspection reports the runtime reference absent. The existing delete operation then anchors a tombstone transaction. Successful lifecycle mutations are durably marked `succeeded/complete`; replaying one returns current instance state without overwriting a newer desired state or repeating runtime work. A pending mutation resumes under the original operation when its idempotency key, type, target, and request hash match; conflicting reuse is rejected. Repeating deletion against a tombstone succeeds without touching the runtime. Missing persistent runtime data is recorded as `runtime_missing` by inspect, start, stop, and restart paths and is not recreated.

Slice 03 serializes lifecycle mutations through a cancellable in-process gate. This guarantees deterministic operation creation and replay for the v0.1 single-`openboxd` deployment. Cross-process execution claims and recovery leases belong to the durable worker/reconciliation design in slice 05 and are not implied by this boundary.

## Consequences

Human instance names can change independently of runtime identity, daemon reconnects can recover the same managed resource, and stale retries cannot delete replacements. VM lifecycle, automatic reconciliation, public APIs, snapshots, and command execution remain outside this slice.

Real-Incus lifecycle tests are opt-in through `OPENBOX_INCUS_TEST_SOCKET`, `OPENBOX_INCUS_TEST_STORAGE`, and `OPENBOX_INCUS_TEST_IMAGE`. They use a temporary project, verify that guest root maps to a nonzero host ID, reconnect through a new adapter, and clean up the container and project resources.
