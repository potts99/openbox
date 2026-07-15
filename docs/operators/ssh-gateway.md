# SSH gateway operations

OpenBox runs its own SSH gateway on TCP port `2222` by default. It does not
modify the host SSH daemon, its configuration, or port 22. Keep host SSH
available as the recovery path.

## Configure and start

The daemon flags are:

```text
--ssh-address :2222
--ssh-host-key /var/lib/openbox/ssh/gateway_host
--ssh-instance-key /var/lib/openbox/ssh/instance_client
--ssh-known-hosts /var/lib/openbox/ssh/known_instances
```

The gateway host key and internal instance client key are generated once as
Ed25519 keys and must remain owner-only files. OpenBox refuses permissive,
non-regular, or symlinked private-key files. Back up both keys with the SQLite
database. Restoring a different gateway host key causes the expected SSH host
identity warning. Losing the instance client key prevents gateway access to
existing instances until that key is restored to their authorized keys.

The internal client key is separate from the public gateway host key and from
owner keys. New instances receive its public half through structured
cloud-init. Instance SSH host keys are pinned on first connection against the
stable runtime identity; a later mismatch is refused.

## Register an owner key

Add at least one owner public key in the authenticated dashboard or through
`POST /v1/ssh-keys`. Only exact registered-key fingerprints authenticate to the
gateway. Unknown keys and malformed usernames are denied and rate limited.

Use the management endpoint without opening a host shell:

```sh
ssh -p 2222 openbox@server.example ls
ssh -p 2222 openbox@server.example new dev --kind vps
ssh -p 2222 openbox@server.example inspect dev
ssh -p 2222 openbox@server.example start dev
```

The allow-listed command language supports `new`, `ls`, `inspect`, `start`,
`stop`, `restart`, `cp`, and `rm`. It is parsed directly into typed application
requests; no input is passed to a host shell. The `cp` protocol is reserved and
becomes active when the cloning service from Slice 11 is configured.

Enter an instance by using its name as the SSH username:

```sh
ssh -p 2222 dev@server.example
```

If `dev` is stopped, OpenBox submits a durable start operation, prints startup
progress, and waits for readiness with a bounded timeout. It then connects only
to a private instance address and proxies PTY allocation, terminal resize,
signals, standard streams, and exit status. It never opens a host shell.

## Optional local aliases

Preview the exact OpenSSH configuration before changing a file:

```sh
openbox ssh-config print --host server.example
```

Install it in `~/.ssh/config`:

```sh
openbox ssh-config install --host server.example
```

Installation refuses an existing `Host openbox` or `Host *.openbox` entry and
never replaces one. The resulting aliases support:

```sh
ssh openbox ls
ssh dev.openbox
```

Use `--alias`, `--port`, or `--config` to choose a different management alias,
gateway port, or client config file.

## Deliberately refused features

The v0.1 gateway refuses SCP, SFTP, SSH agent forwarding, X11 forwarding, TCP
forwarding, Unix-stream forwarding, and unknown subsystems or channel types.
Use the native API for management and a later explicit transfer feature for
files. Refused probes do not start a stopped instance.

Authentication, pending handshakes, active connections, and session channels
all have bounded default limits. Audit records contain the owner-key
fingerprint, remote IP, command name, target, outcome, and timestamp. They do
not contain credentials, command arguments, terminal contents, or file data.

## Failure and recovery

If port 2222 is already occupied, `openboxd` exits with a listener error and
does not disturb the existing listener. Choose another `--ssh-address` and
update the local alias. An SSH gateway failure does not stop running instances
and does not affect host SSH on port 22.

Treat an unexpected instance host-key mismatch as a security or recovery
event. Verify the instance identity in Incus before removing its pinned entry.
Do not delete the whole known-hosts file as a routine workaround.
