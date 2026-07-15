# Runtime recovery

OpenBox treats SQLite metadata as the record of intent and Incus as observed runtime state. During an Incus outage, runtime mutations stop and the service is degraded read-only; metadata remains available.

## Diagnostics

- `runtime_missing` means a durable persistent instance has no runtime resource at its recorded reference. OpenBox will not recreate it automatically.
- `replacement_identity` means that reference now points to a resource with different or incomplete OpenBox identity metadata. OpenBox leaves it untouched.
- `unmanaged_resource` means Incus contains a resource with no matching active OpenBox record. OpenBox leaves it untouched.

## Explicit actions

- **Restore** only after selecting the correct snapshot or backup. The restore implementation must recreate the recorded instance identity and isolation. OpenBox verifies both before accepting it.
- **Adopt** after restoring OpenBox metadata when the matching, correctly labelled runtime resource still exists. Identity and isolation must match exactly.
- **Forget** when the persistent runtime is intentionally gone and its durable OpenBox record should be tombstoned. This uses the normal identity-safe deletion path.

Never resolve `runtime_missing` by creating an empty instance at the same reference. Doing so can hide data loss and will be rejected as an identity conflict unless it carries the original metadata; even then, only the explicit restore path is intended for recovery.
