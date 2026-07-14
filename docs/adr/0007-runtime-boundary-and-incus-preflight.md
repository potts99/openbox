# ADR 0007: Runtime boundary and local Incus preflight

- Status: Accepted
- Date: 2026-07-15

## Context

OpenBox must support both containers and KVM-backed virtual machines without coupling application services to Incus response types. Host validation and bootstrap also need stronger safety rules than ordinary runtime lifecycle calls.

## Decision

The `internal/runtime` package owns a provider-neutral lifecycle contract. A deterministic fake implements the complete contract for application tests. In this slice, the real Incus adapter implements capability discovery and managed bootstrap only; later lifecycle slices will add instance mutation.

The adapter communicates exclusively through an absolute local Unix-socket path using context-aware HTTP requests with bounded timeouts. Bootstrap manages a dedicated project, bridge, and profiles carrying explicit OpenBox ownership labels. Existing resources without matching labels are conflicts and are never mutated.

## Consequences

Application code can test lifecycle and failures without a daemon, while real-host tests remain explicit and isolated. OpenBox cannot accidentally connect to remote Incus endpoints or adopt similarly named administrator resources. The selected storage pool remains administrator-owned and is only referenced by managed profiles.
