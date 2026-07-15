# Networking security boundaries

## Guest root cannot relax host policy

OpenBox applies each instance's network ACLs to the Incus-managed bridge NIC
from `openboxd`, using the host's Incus Unix socket. The Incus adapter rejects
remote HTTP(S) endpoints, so ACL, managed-bridge, and per-instance NIC policy
changes require access to that host-local socket.

OpenBox profiles expose only the guest root filesystem and its `eth0` NIC.
They do not mount the Incus socket, the OpenBox state database, or any other
host policy asset into a guest. Containers are explicitly unprivileged
(`security.privileged=false`); VMs likewise receive no Incus socket.

Guest root may change routes, DNS settings, or firewall rules inside its own
network namespace. Those changes cannot alter the host bridge configuration or
the Incus ACL attached to the guest NIC, so they cannot expand egress beyond
the host-side policy. DNS allowlist resolution also runs in the host process;
guest DNS configuration cannot modify its results.

This boundary does not cover guest eBPF, TLS interception, or cross-host
policy.

## Operator-visible policy status

Instance inspect responses expose `network_policy` without logging or returning
packet payloads or DNS answers. The field reports:

- `egress_mode`: the effective `standard` or `restricted` mode;
- `acls`: the Incus ACL names attached to the instance NIC;
- `resolution`: hostname allowlist state (`idle`, `pending`, `resolved`, or
  `failed`) and hostname names only, never resolved IP addresses; and
- `denied_flows`: a best-effort counter of host-side policy application
  failures that fail closed, such as an Incus ACL update failure.

`openbox inspect` displays the same egress mode, ACL names, resolution state,
and denied-flow counter. The counter is in-memory and resets when `openboxd`
restarts; it is operational telemetry, not an audit trail.
