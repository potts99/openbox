# Software catalog

Curated guest packages live in `internal/software/`.

| Path | Role |
|---|---|
| `catalog.go` | Package/pin types, `DefaultCatalog`, validation |
| `pi.go` | Pi + tmux + Node 22 pins from the Devbox definition |
| `node22.go` | NodeSource Node 22 apt repo setup via WriteFile |
| `herdr.go` | Herdr github-release pins (version + per-arch sha256) |
| `release.go` | Release URL, fetch, digest verify |
| `install.go` | Guest Exec recipes; host-fetch + WriteFile for github-release |

## Adding or bumping Herdr

1. Read the GitHub release asset digests for the target tag (linux x86_64 and
   aarch64 only).
2. Update `internal/software/herdr.go` version + both `SHA256` values (64-char
   hex, no `sha256:` prefix).
3. Keep `Verify` as `herdr --version`.
4. Never add `curl`/`wget`/raw `https` argv steps; release install is host-side.

## Node.js (Pi package)

The `pi` catalog package pins **Node 22** (`22.23.1-1nodesource1` from
NodeSource). OpenBox writes the apt keyring and `sources.list.d` entry via
`WriteFile`, then installs the pinned `nodejs` package before `npm install -g`
for Pi. Guest recipes never run `curl|sh`.

Bump Node by updating `images/devbox/devbox.json` (`nodejs` apt pin) and
re-testing `pi --version` on both `x86_64` and `aarch64`.

## Guest writes (default)

Guest file content uses `runtime.WriteFile` (Incus `/instances/{ref}/files`).
Do not route binaries or config bodies through `Exec` Stdin — that path
base64-wraps argv and hits Incus's 1 MiB non-large request limit.

## Install path (github-release)

`software.Install` with `InstallOptions.Architecture` selects the asset,
fetches on the host, verifies sha256, `WriteFile`s to
`/usr/local/bin/<id>.openbox-tmp` (mode `0755`), `mv`s into place, then runs
verify steps.
