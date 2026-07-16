# Isolation: strong (VM) and container

OpenBox instances are either KVM-backed Incus **virtual machines** (`strong`) or unprivileged Incus **system containers** (`container`). OpenBox never labels a container as a VM and never silently downgrades an explicit `strong` request.

## Selection policy

Public `requested_isolation` values:

| Request | Result |
|---------|--------|
| `strong` | Incus virtual machine. Fails before create if KVM / VM support is unavailable. |
| `container` | Unprivileged system container (even when KVM is available). |
| *omitted* | `strong` if KVM is usable, otherwise `container`. |

There is no `best_available` or `standard` request value.

The selected runtime type is stored as `actual_isolation` (`virtual_machine` or `container`) and is verified on later lifecycle actions.

`openbox doctor` distinguishes an absent `/dev/kvm`, permission denial, unavailable nested virtualization, and an Incus daemon without VM support. A container-capable host remains usable when strong isolation is unavailable; default creates then use `container`.

## Fast path

On KVM + ZFS hosts, sandbox creates prefer a **VM warm pool** (golden VM snapshot + CoW clones). On hosts without KVM, the existing **system-container warm pool** remains the fast path. See the VM-first isolation design for details.

## VM image requirements

VM creation requires an immutable Incus image fingerprint for a `virtual-machine` image that advertises cloud-init compatibility. OpenBox resolves aliases before creating an operation and passes only the resulting fingerprint to Incus.

The VM receives structured Incus configuration for:

- CPU and memory limits;
- a root disk on the configured storage pool;
- an explicit `cloud-init:config` disk;
- an interface on the managed OpenBox network;
- the owner's SSH public key through cloud-init;
- OpenBox ownership, owner, and instance identity labels.

No VM setting is constructed as a shell command.

## Readiness and failure handling

After starting a VM, OpenBox waits first for the guest agent to report a non-loopback address and then for TCP port 22 to accept a connection. Both phases are cancellable and bounded by the configured readiness timeout. Operations expose `waiting_for_agent` and `waiting_for_ssh` stages.

If a newly created VM fails before readiness, OpenBox inspects its immutable instance identity before stopping or deleting it. It inspects again between stop and delete. If the runtime resource has been replaced, changed type, or lost its ownership labels, cleanup stops rather than touching the replacement.

## Real-KVM integration test

The default suite uses deterministic fake and Unix-socket HTTP runtimes. To opt into the real VM lifecycle and restart test:

```sh
OPENBOX_INCUS_TEST_SOCKET=/var/lib/incus/unix.socket \
OPENBOX_INCUS_TEST_STORAGE=default \
OPENBOX_INCUS_TEST_VM_IMAGE=<immutable-vm-fingerprint> \
  go test ./internal/runtime/incus -run TestRealIncusVMLifecycleAndRestart -v
```

The host must expose working KVM, and the pinned image must contain Incus agent support, cloud-init, and an SSH service. The test creates a uniquely named project, bridge, profiles, and VM, exercises two boot/readiness cycles, and removes only its labelled test resources.
