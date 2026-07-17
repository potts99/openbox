# OpenBox

OpenBox is a self-hosted platform for persistent VPS instances, disposable AI-agent sandboxes, and reusable development environments.

The project is under active development. Before v0.1, commands, APIs, configuration, data formats, and upgrade paths may change without compatibility guarantees.

## Monorepo layout

| Path | Purpose |
|------|---------|
| `cmd/`, `internal/`, `pkg/` | Go control plane, CLI, SDK |
| `apps/web` | Owner console SPA (embedded into `openboxd`) |
| `apps/marketing` | Public marketing site (Cloudflare Pages — not self-host) |
| `deploy/` | Self-host install kit (systemd, Caddy for the console) |
| `examples/` | Agent workflow recipes |

## Development

Install Go 1.24 or newer, Node.js 22, pnpm 10, and GNU Make. Then run:

```sh
pnpm install --frozen-lockfile
make check
```

`make check` reproduces the formatting, linting, test, policy, generated-file, and build checks run by CI.

## Host preflight

With `openboxd` running on the host, inspect container and VM readiness through
the private versioned API:

```sh
openbox doctor
openbox doctor --json
```

Missing KVM is reported as unavailable rather than fatal because OpenBox supports container-only hosts. The daemon owns Incus socket configuration; the CLI never accesses Incus or SQLite directly. See [the runtime preflight guide](docs/runtime-preflight.md) for socket overrides, bootstrap safety, and integration testing. New hosts should follow the [install guide](docs/operators/install.md) and [host matrix](docs/operators/host-matrix.md).

See [the strong-isolation operator guide](docs/operators/strong-isolation.md) for VM selection, image requirements, readiness, and the opt-in KVM test. Sandbox create, exec, TTL, and expiry are covered in [the sandbox operator guide](docs/operators/sandbox.md) and [sandbox lifecycle](docs/api/sandbox-lifecycle.md).

The API and embedded owner console default to loopback, with first-admin
username/password setup, browser sessions, and scoped API tokens. A non-loopback
listener requires TLS. See the [private API operator guide](docs/operators/private-api.md),
[API v1 behavior](docs/api/v1.md), and
[owner-authentication security model](docs/security/owner-authentication.md).

The SSH-native management and instance gateway listens on the separate,
configurable port 2222 and never changes host SSH. See the
[SSH gateway operator guide](docs/operators/ssh-gateway.md) for owner-key
registration, management commands, direct instance sessions, optional local
aliases, denied forwarding modes, and recovery behavior.

Optional HTTPS routes (private by default) and `openbox forward` tunnels are
documented in the [HTTPS routes operator guide](docs/operators/https-routes.md).

## Security

Do not report vulnerabilities in a public issue. See [SECURITY.md](SECURITY.md) for private reporting instructions.

## License

OpenBox is licensed under the GNU Affero General Public License v3.0 only. See [LICENSE](LICENSE).
