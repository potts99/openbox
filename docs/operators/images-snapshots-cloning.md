# Images, snapshots, and VPS cloning

OpenBox pins images by digest, snapshots instances through durable operations,
protects designated VPS bases, and clones with verified runtime identity.

## Images

Aliases resolve to immutable fingerprints at create time. Updating an alias
affects **future** creates only; existing instances keep their pinned
`image_id` (fingerprint).

Curated manifests live in `internal/images` for general and sandbox aliases
(plus a legacy Devbox alias retained only as a pin carrier for the software
catalog). Install agent tooling via the **software catalog** on VPS instances.

## Snapshots

```text
snapshot.create  → Incus snapshot
snapshot.delete  → remove Incus snapshot + DB row
snapshot.restore → restore-as-new independent instance
```

Restore-as-new and `cp` both rewrite `user.openbox.*` ownership metadata on the
copy and refuse to complete if runtime identity does not match the new instance.

## Protection

VPS instances can be protected. While `protected` is true, delete submissions
fail with `protected_base`. Clear protection explicitly before deletion.
Sandboxes cannot be protected.

```go
service.SetProtection(ctx, owner, id, true)  // mark base
service.SetProtection(ctx, owner, id, false) // allow delete
```

## Cloning (`cp`)

```sh
ssh -p 2222 openbox@host cp base feature
```

`cp` records provenance (`clone_source_*` columns) **without** foreign keys, so
deleting the source instance or snapshot never invalidates a completed clone.

### Warnings (before execution)

| Condition | Warning |
|---|---|
| Storage drivers lack CoW (`dir`, etc.) | Full copy; OpenBox will not claim copy-on-write |
| Source has Pi installed and is not protected | Guest files may include secrets |

CoW is only claimed when capabilities advertise a driver such as `zfs`,
`btrfs`, `lvm`, or `ceph`.

## Acceptance checks

- Alias moves do not change existing instance `image_id`
- Clone starts/stops after source and snapshot deletion
- Copy never completes with the source's `user.openbox.instance_id`
- Protected bases cannot be deleted until unprotected
