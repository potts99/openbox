# SSH gateway development notes

The SSH slice keeps four boundaries separate:

- `internal/sshgateway/commands` is a strict, non-shell parser for the public
  management command language.
- `internal/app/sshcommands` maps typed commands to owner-scoped application
  services and durable mutations.
- `internal/sshgateway` owns SSH authentication, limits, request refusal,
  auditing, and channel/session proxy mechanics.
- `internal/sshgateway/proxy` owns durable instance start/readiness and the
  downstream SSH client. It resolves only private addresses and pins guest host
  keys by stable runtime identity.

Handlers do not call Incus directly. The downstream proxy depends on narrow
service and address-resolver interfaces, and its private key never crosses the
proxy boundary. The public gateway host key and internal instance client key
must always use different files.

Management command parsing rejects control characters, shell metacharacters,
expansion syntax, unsupported flags, malformed quoting, oversized input, and
invalid typed values. Adding a command requires a new closed command type,
parser tests, fuzz seeds, application mapping, output tests, and an audit-safe
command name. Never introduce `sh -c`, shell word expansion, or string-built
host commands.

Instance sessions are lazy: unsupported subsystem or transfer probes are
rejected before `EnsureReady`, so they cannot start a stopped instance.
Accepted shell and exec sessions forward PTY settings, resize events, signals,
streams, and exit status through `RemoteSession`. TCP, agent, X11, streamlocal,
SCP, and SFTP remain deny-by-default.

Run focused checks with:

```sh
go test -race ./internal/sshgateway/... ./internal/app/sshcommands ./cmd/openboxd
go test -count=50 ./internal/sshgateway/... ./internal/app/sshcommands
go test -fuzz=Fuzz -fuzztime=10s ./internal/sshgateway/commands
```

The transport integration suite uses in-process SSH clients and fake instance
sessions, so it runs without Incus. Keep real-Incus coverage opt-in until the CI
host provides the managed private network and compatible images.
