# Images, snapshots, and cloning — development notes

Slice 11 packages:

| Package / path | Role |
|---|---|
| `internal/images` | Alias→fingerprint resolve; curated manifests |
| `internal/snapshots` | Durable snapshot create/list/delete/restore-as-new |
| `internal/app/clones` | Durable `instance.copy` (`cp`), CoW/secrets warnings, identity verify |
| `internal/runtime` | `CreateSnapshot`, `DeleteSnapshot`, `CopyInstance` (+ metadata rewrite) |
| `internal/persistence/migrations/006_clone_provenance.sql` | Provenance columns (no FK) |
| `internal/app/sshcommands` | SSH `cp` → clone service; prints warnings |

## Safety invariants

- Image aliases pin fingerprints per instance at create time.
- Copy/restore rewrite ownership metadata before completion; identity mismatch
  is integrity-threatening (`IdentityConflictError`).
- Provenance is historical text, not a live FK — source deletion is safe.
- `StorageEfficientCopy` must be true before any copy-on-write claim.
- Unprotected Devbox clones surface a secrets warning before execution.

## Tests to run

```sh
go test ./internal/images/ ./internal/snapshots/ ./internal/app/clones/ \
  ./internal/runtime/fake/ ./internal/runtime/incus/ \
  ./internal/app/instances/ ./internal/app/sshcommands/ ./internal/persistence/sqlite/
```
