---
title: "Slice 17 — Installer, upgrade, backup, and recovery"
status: planned
milestone: "M6 v0.1 hardening"
depends_on: ["08-ssh-command-and-instance-gateway", "10-https-routes-and-optional-domains", "16-provider-adapters-and-pi-gateway-package"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 17 — Installer, upgrade, backup, and recovery

## Goal

Make the assembled system installable and recoverable on the supported Debian-family host matrix without risking host access.

## Dependencies

- [08-ssh-command-and-instance-gateway](08-ssh-command-and-instance-gateway.md)
- [10-https-routes-and-optional-domains](10-https-routes-and-optional-domains.md)
- [16-provider-adapters-and-pi-gateway-package](16-provider-adapters-and-pi-gateway-package.md)

## Non-goals

- No unattended host firewall takeover.
- No non-Linux installer.
- No multi-host restore.

## Proposed files

- `deploy/install/`
- `deploy/systemd/`
- `deploy/caddy/`
- `cmd/openbox/doctor.go`
- `cmd/openbox/backup.go`
- `cmd/openbox/restore.go`
- `docs/operators/`

## Test-first implementation tasks

1. [ ] Build installation tests in disposable VMs for Debian 13 and Ubuntu 24.04 LTS with a compatible HWE kernel and pinned Incus LTS packages.
2. [ ] Preflight root access, ports, disk, kernel, Incus, namespaces, cgroups, network tooling, and optional KVM before mutation.
3. [ ] Install services with hardened systemd units, dedicated users, explicit file permissions, and host SSH left untouched.
4. [ ] Make every installer step idempotent and write a machine-readable installation report.
5. [ ] Implement configuration validation and atomic service reloads.
6. [ ] Back up OpenBox SQLite before migrations and validate schema compatibility before replacing binaries.
7. [ ] Implement metadata export/restore, separate gateway store/key backup, and documented Incus storage backup hooks.
8. [ ] Implement orphan adoption using OpenBox labels and stable runtime identity; require explicit confirmation.
9. [ ] Test upgrade interruption and document rollback boundaries for binaries, schemas, Caddy config, and images.

## Verification

- [ ] Fresh install and second-run idempotency tests.
- [ ] No-KVM, KVM, occupied-port, bad-DNS, low-disk, and partial-package tests.
- [ ] Backup/restore with correct, absent, and mismatched gateway key.
- [ ] Upgrade interruption at every state-changing step.

## Acceptance gate

- [ ] Installation never changes host SSH configuration by default.
- [ ] A failed install reports exactly what changed and how to retry or remove it.
- [ ] Metadata, gateway credentials, and guest data have separate, tested recovery instructions.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
