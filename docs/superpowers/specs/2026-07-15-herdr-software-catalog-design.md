# Herdr software catalog package

**Date:** 2026-07-15  
**Status:** approved for planning  
**Depends on:** [VPS software catalog](2026-07-15-vps-software-catalog-design.md)  
**Approach:** curated `herdr` package via host-fetched GitHub release pins

## Summary

Add **Herdr** ([herdr.dev](https://herdr.dev)) as a second curated OpenBox software catalog package. Users install it at VPS create or later from the Software panel, then run `herdr` from the normal Terminal. Install uses a new `github-release` pin manager: OpenBox downloads the pinned binary on the **host**, verifies sha256, and writes it into the guest through existing `Exec` + `Stdin`. Guest recipes still ban `curl`/`wget`/raw remote script steps.

## Goals

- Catalog package `herdr` installable the same way as `pi`.
- Exact, auditable pins (version + per-arch asset + sha256).
- Host-side fetch and digest verification; guest does not need GitHub egress for install.
- Architecture-aware asset selection (`x86_64` / `aarch64` Linux).
- Docs for install + terminal usage; optional pairing with Pi.

## Non-goals

- Replacing tmux or rewiring Launch Pi / session attach to Herdr.
- Auto-starting Pi (or any agent) inside Herdr on install.
- Managed `herdr update` / preview channel from OpenBox.
- Sandbox software installs (deferred with the parent catalog follow-up).
- Uninstall / package remove.
- macOS or Windows guest assets (OpenBox guests are Linux).

## Product model

| Surface | Behavior |
|---|---|
| Catalog | `GET /v1/software` includes `herdr` beside `pi`. |
| Create | Optional `packages: ["herdr"]` (UI checkbox). |
| After create | Software panel: Install + status/error. |
| Run | Normal Terminal; user runs `herdr`. |
| With Pi | Both may be installed; no conflict. Optional docs note: `herdr integration install pi`. |

No dedicated Launch Herdr control.

## Catalog entry

OpenBox-owned, versioned in-repo (mirror the `pi` package style):

- `id`: `herdr`
- `name` / `description`: agent multiplexer CLI for persistent terminal sessions
- Pins: manager `github-release` with:
  - repository `ogulcancelik/herdr`
  - exact version (initial pin: `0.7.4`)
  - Linux assets only:
    - `x86_64` → `herdr-linux-x86_64` + sha256
    - `aarch64` → `herdr-linux-aarch64` + sha256
- `verify`: `["herdr", "--version"]`
- Install delivery is **not** a guest argv download; see Install runtime below.
- Destination path: `/usr/local/bin/herdr`, mode `0755`.

Pin bumps are deliberate code changes (same discipline as Pi apt/npm pins). Digests must match the GitHub release asset digests for that tag.

### Validation rules

- `github-release` is an allowed pin manager alongside `apt` and `npm`.
- Version must be exact (no ranges, `latest`, or channel tags).
- Every supported guest arch used by OpenBox must have a pinned asset + sha256.
- Recipe argv steps still reject `curl`, `wget`, and embedded `http://` / `https://` strings.
- Official `curl … \| sh` installer is never used.

## Install runtime

Extend software install (not the generic argv-only path alone):

1. Resolve guest architecture from instance/runtime capabilities (`x86_64` or `aarch64`). Unsupported arch → fail with a clear error.
2. Select the pinned asset for that arch.
3. On the **host**, HTTP GET the canonical GitHub release URL for that tag/asset (`https://github.com/ogulcancelik/herdr/releases/download/v<version>/<asset>`).
4. Verify the response body against the pinned sha256; mismatch or empty body → fail closed (`failed`).
5. Guest `Exec` steps (argv-only, no shell), with `Stdin` carrying the verified bytes on the write step:
   - write to `/usr/local/bin/herdr.openbox-tmp` via `tee`;
   - `chmod` `0755` on the temp path;
   - `mv` temp → `/usr/local/bin/herdr`.
6. Run verify steps (`herdr --version`).
7. Persist instance software status `installed` or `failed` as for other catalog packages.

Downloader is injectable for tests (fake HTTP / preloaded bytes). No new Incus file-push API; reuse `ExecRequest.Stdin`.

Idempotent re-install: overwrite binary + re-verify; successful verify → `installed`.

## Data / API / UI

No new endpoints beyond the parent software catalog:

- Instance `software[]` rows use `package_id=herdr`.
- Create `packages` and `POST …/software/{package_id}/install` accept `herdr` when present in `DefaultCatalog()`.
- Console Software panel and create checkboxes list both packages.

Authz unchanged (owner-scoped).

## Documentation

- Operators/development: document Herdr as a catalog package, pin/update process, and terminal usage.
- Note optional Pi integration when both packages are installed.
- Do not document Launch Herdr or tmux replacement.

## Testing

- Catalog includes `herdr`; `Validate` accepts github-release pins and still rejects remote-script recipes.
- Install selects correct asset by arch; records stdin install + verify against a fake execer/downloader.
- Digest mismatch → error / `failed` status; no partial “installed” success.
- API catalog list returns `herdr`; create/install with `herdr` exercises the same service path as `pi` with a stubbed release fetch.

## Rollout

1. Extend pin validation + install path for `github-release`.
2. Add `herdr` package definition with v0.7.4 Linux digests.
3. Wire catalog list / create / Software UI copy.
4. Docs + pin-bump note.

## Open follow-ups (explicitly later)

- Sandbox + software.
- Uninstall.
- Replacing tmux persistence with Herdr for agent sessions.
- Additional github-release catalog packages reusing the same manager.
