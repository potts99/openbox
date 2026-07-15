# Software catalog

Curated guest packages live in `internal/software/`.

| Path | Role |
|---|---|
| `catalog.go` | Package/pin types, `DefaultCatalog`, validation |
| `pi.go` | Pi + tmux pins from the Devbox definition |
| `herdr.go` | Herdr github-release pins (version + per-arch sha256) |
| `release.go` | Release URL, fetch, digest verify |
| `install.go` | Guest Exec recipes; host-fetch path for github-release |

## Adding or bumping Herdr

1. Read the GitHub release asset digests for the target tag (linux x86_64 and
   aarch64 only).
2. Update `internal/software/herdr.go` version + both `SHA256` values (64-char
   hex, no `sha256:` prefix).
3. Keep `Verify` as `herdr --version`.
4. Never add `curl`/`wget`/raw `https` argv steps; release install is host-side.

## Install path (github-release)

`software.Install` with `InstallOptions.Architecture` selects the asset,
fetches on the host, verifies sha256, then guest-execs `tee` → `chmod 0755` →
`mv` into `/usr/local/bin/<package-id>` and runs verify steps.
