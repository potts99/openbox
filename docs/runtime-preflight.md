# Runtime preflight and managed bootstrap

OpenBox talks directly to the local Incus REST API over a Unix socket. It never invokes the `incus` command-line client and does not accept a remote URL for this connection.

## Check a host

The default socket is `/var/lib/incus/unix.socket`:

```sh
openbox doctor
openbox doctor --json
openbox doctor --socket /run/incus/unix.socket --timeout 15s
```

Doctor checks the daemon version and architecture, required Linux namespaces, cgroups, supported storage drivers, host networking tools, accessible `/dev/kvm`, and Incus virtual-machine support. A host without KVM remains usable for standard container isolation; strong isolation is clearly reported as unavailable.

Fatal results prevent safe container operation. Warnings identify optional or repairable host tooling. JSON output uses stable `pass`, `warning`, `unavailable`, and `fatal` status strings for automation.

## Managed bootstrap

The internal bootstrap operation creates only a named OpenBox project, project-scoped bridge, and container/VM profiles. The administrator must select an existing storage pool; OpenBox references it but does not take ownership of it.

Every resource OpenBox creates carries these Incus configuration labels:

```text
user.openbox.managed=true
user.openbox.resource=<resource-kind>
```

Bootstrap is idempotent. If a requested name already exists without matching ownership labels, OpenBox stops and tells the operator to rename that resource or choose another OpenBox name. If a labelled resource has drifted in a required project feature, bridge setting, network type, or profile device, OpenBox reports the exact fields to restore instead of overwriting them. Incus-added and other unrelated extra fields are preserved. OpenBox never adopts, edits, or deletes an unknown resource.

## Real-Incus integration tests

The default test suite uses a Unix-socket test daemon and does not require Incus. To opt into read-only preflight against a real daemon:

```sh
OPENBOX_INCUS_TEST_SOCKET=/var/lib/incus/unix.socket \
  go test ./internal/runtime/incus -run TestRealIncusPreflightAndBootstrap -v
```

Set `OPENBOX_INCUS_TEST_STORAGE` as well to test bootstrap twice in an isolated, temporary OpenBox test project. The test verifies that the unrelated `default` project is unchanged and removes only the resources it created.
