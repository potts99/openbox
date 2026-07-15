# ADR 0008: Capability-driven KVM VM lifecycle

- Status: Accepted
- Date: 2026-07-15

## Context

OpenBox needs strong hardware isolation without changing the application lifecycle contract or misrepresenting container isolation. VM boot also has readiness and cleanup risks that do not exist for an already-running container.

## Decision

The application selects actual isolation once, before durable operation creation. Standard always selects an unprivileged container. Strong requires an explicitly usable VM capability. Best available selects a VM only when `/dev/kvm` is accessible and Incus advertises VM support; otherwise it selects a container. The actual selection is persisted and verified on every lifecycle action.

The Incus adapter accepts only pinned, cloud-init-compatible VM image fingerprints. It constructs VM root, cloud-init, network, resource, SSH-key, and identity configuration as structured REST API data. VM readiness is a bounded, cancellable wait for an agent-reported address followed by SSH port readiness.

A failed newly created VM is cleaned up only after identity and type verification. OpenBox rechecks identity between stopping and deletion, refusing cleanup if another resource has replaced it.

## Consequences

Callers use the same create, inspect, start, stop, restart, and delete application methods for both isolation types. Explicit strong requests never downgrade. VM operations can take longer and expose agent and SSH readiness stages. Real-KVM verification remains opt-in because ordinary CI and contributor machines may not expose nested virtualization.
