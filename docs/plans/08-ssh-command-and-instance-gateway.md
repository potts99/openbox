---
title: "Slice 08 — SSH command and instance gateway"
status: planned
milestone: "M2 SSH and web access"
depends_on: ["05-durable-operations-and-reconciliation", "07-owner-auth-and-dashboard-shell"]
spec: "../specs/2026-07-14-openbox-design.md"
---

# Slice 08 — SSH command and instance gateway

## Goal

Provide the SSH-native OpenBox experience without taking over host recovery access or exposing a host shell.

## Dependencies

- [05-durable-operations-and-reconciliation](05-durable-operations-and-reconciliation.md)
- [07-owner-auth-and-dashboard-shell](07-owner-auth-and-dashboard-shell.md)

## Non-goals

- No SCP/SFTP in the first slice.
- No port-22 takeover by default.
- No parsing through a shell.

## Proposed files

- `internal/sshgateway/`
- `internal/sshgateway/commands/`
- `internal/sshgateway/proxy/`
- `cmd/openboxd/`
- `cmd/openbox/ssh_config.go`

## Test-first implementation tasks

1. [ ] Write parser tests and fuzz targets for every supported command before starting an SSH server.
2. [ ] Run a Go SSH server on configurable port 2222 with a generated stable gateway host key.
3. [ ] Authenticate only registered owner public keys and audit fingerprint, command, target, and outcome.
4. [ ] Map `openbox@host` commands to typed application requests for new, ls, inspect, start, stop, restart, cp, and rm.
5. [ ] Map `instance@host` sessions to the named instance; start a stopped instance through a durable operation and wait with a timeout.
6. [ ] Proxy PTY, resize, signals, and exit status to the instance’s internal SSH service without exposing internal gateway credentials.
7. [ ] Refuse subsystems and forwarding modes not explicitly implemented.
8. [ ] Add CLI support for printing or installing optional SSH config aliases without overwriting user entries.

## Verification

- [ ] Parser unit and fuzz tests.
- [ ] Unknown key, malformed username, command injection, port conflict, and rate-limit tests.
- [ ] End-to-end command and interactive shell tests against a container.
- [ ] Host SSH remains reachable after OpenBox gateway failure.

## Acceptance gate

- [ ] No SSH input reaches a host shell.
- [ ] The default installer never changes the host SSH port or configuration.
- [ ] A stopped instance can be entered with one SSH command and reports startup progress.

## Slice boundary

This slice is complete only when its tests, operator/developer documentation, and acceptance gate pass. Do not pull work from later slices merely because an interface leaves room for it.
