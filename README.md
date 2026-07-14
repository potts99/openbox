# OpenBox

OpenBox is a self-hosted platform for persistent VPS instances, disposable AI-agent sandboxes, and reusable development environments.

The project is under active development. Before v0.1, commands, APIs, configuration, data formats, and upgrade paths may change without compatibility guarantees.

## Development

Install Go 1.24 or newer, Node.js 22, pnpm 10, and GNU Make. Then run:

```sh
pnpm install --frozen-lockfile
make check
```

`make check` reproduces the formatting, linting, test, policy, generated-file, and build checks run by CI.

## Host preflight

On a Linux host with Incus installed, inspect container and VM readiness through the local Unix socket:

```sh
openbox doctor
openbox doctor --json
```

Missing KVM is reported as unavailable rather than fatal because OpenBox supports container-only hosts. See [the runtime preflight guide](docs/runtime-preflight.md) for socket overrides, bootstrap safety, and integration testing.

## Security

Do not report vulnerabilities in a public issue. See [SECURITY.md](SECURITY.md) for private reporting instructions.

## License

OpenBox is licensed under the GNU Affero General Public License v3.0 only. See [LICENSE](LICENSE).
