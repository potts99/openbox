# Software catalog

OpenBox installs curated packages into VPS instances through the software
catalog (`GET /v1/software`). Packages are selected at create time
(`packages: [...]`) or later with
`POST /v1/instances/{id}/software/{package_id}/install`. Users run installed
tools from the normal instance Terminal — there is no Launch control for
catalog packages.

## Packages

| ID | What it installs | How to run |
|---|---|---|
| `pi` | Pi coding agent + tmux (Node 22 + exact apt/npm pins) | `pi` / `tmux` |
| `herdr` | Herdr agent multiplexer (GitHub release binary) | `herdr` |

Both may be installed on the same instance. With Pi present, you can optionally
run `herdr integration install pi` inside the guest for richer agent state.

## Herdr pins

Herdr uses the `github-release` manager. OpenBox downloads
`ogulcancelik/herdr` release assets on the **host**, verifies sha256, and
pushes `/usr/local/bin/herdr` into the guest via the Incus files API. The
guest does not run `curl|sh` and does not need GitHub egress for install.

Current pin: **0.7.4** (`herdr-linux-x86_64` / `herdr-linux-aarch64`).

OpenBox does not manage `herdr update` or preview channels. Bump the pin in
`internal/software/herdr.go` when adopting a new release (update version and
both digests from the GitHub release asset digests).

## Pi profiles

Install the `pi` package before applying an owner Pi profile. See
[Pi profiles](pi-profile-and-launcher.md).
