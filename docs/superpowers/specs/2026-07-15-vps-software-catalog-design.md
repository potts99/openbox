# VPS software catalog (collapse Devbox)

**Date:** 2026-07-15  
**Status:** approved for planning  
**Approach:** soft collapse — VPS + curated software catalog; no Launch Pi button

## Summary

OpenBox stops treating “plain VPS” vs “Pi Devbox” as separate product lines. Persistent instances are **VPS**. Users select **software from an OpenBox catalog** at create time and anytime after. **Pi** (pinned coding agent + tmux) is the first package. Users run tools from the normal **Terminal**; there is no Launch Pi control. **Sandbox** stays disposable. There are no production users yet — **hard-cut** `devbox` with no migration shims.

## Goals

- One persistent kind: `vps` (plus existing `sandbox`).
- Catalog-driven software selection at **create** and **later**.
- Pi remains a first-class optional package (not a separate kind).
- Remove Launch Pi UI; terminal-only usage.
- Drop `devbox` as a kind (no users yet — hard cut, no migration/compat shims).
- Protect/clone available on VPS (no longer Devbox-only).

## Non-goals

- Full arbitrary package manager / free-form apt UI.
- Disk rebuild or image swap as the way to add software after create.
- Changing Sandbox TTL or sandbox exec policy.
- Removing Pi **profiles** (shared settings apply remains; install Pi first).
- Generic multi-package dependency solver beyond what the first catalog entries need.

## Current state (problem)

- Kinds: `vps` | `devbox` | `sandbox`.
- Curated images: general / sandbox / `openbox:devbox/...` with `IncludesPi` only on Devbox.
- Launch Pi gated by kind in the web UI; Devbox is the Pi-preloaded path.
- No control-plane install API — software is image-at-create or manual guest apt.
- Protect/clone gated to Devboxes.

## Product model

| Surface | Behavior |
|---|---|
| Kind | Create only `vps` or `sandbox`. |
| Software | OpenBox catalog packages (start with `pi`). |
| Create | Optional `packages: ["pi"]` (UI checkbox). |
| After create | Instance **Software** panel: install (v1); status visible. |
| Run Pi | Normal terminal; user runs `pi` / tmux as they wish. |
| Profiles | `/v1/pi-profiles` apply still materializes settings into guests. |

## Data model

### Catalog entry

OpenBox-owned, versioned in-repo (evolve from `images/devbox/devbox.json` pins):

- `id` (e.g. `pi`)
- `name`, `description`
- `pins[]` — exact manager/name/version (apt/npm)
- `install` / `verify` steps — **data recipes**, not caller-supplied shell strings
- optional `conflicts`

### Instance software state

Persisted per instance, exposed on instance read:

```text
package_id, status (absent|pending|installed|failed), version?, updated_at, error?
```

- Create with `packages` → after instance ready, drive install to `pending` then `installed`/`failed`.
- Idempotent: successful verify while already present → `installed`.

### Kind cutover

- Remove `devbox` from create/API enums and domain validation (hard cut; no
  runtime migration of existing rows — there are no production users yet).
- Local/dev DBs with leftover `devbox` rows may be wiped or manually updated;
  do not ship fallback mappers.
- Docs and OpenAPI enums updated; `IncludesPi` on image manifests becomes
  informational or retired in favor of instance software state.

### Protect / clone

- Allow protect and clone on **VPS** (policy previously Devbox-only).
- Secrets warning for clones that may contain `~/.pi/agent/auth` remains when Pi-related paths exist.

## Install runtime

Hybrid:

1. **Create** — default general Ubuntu image (not Devbox alias).
2. **Add-ons** — after ready, Incus guest `Exec` runs catalog recipe (pinned installs + verify).
3. No host shell; argv-only; exact pins; failed verify → `failed` + error message.
4. Prefer durable **operation** (or operations worker task) so refresh-safe; UI polls instance `software` and/or operation events.

Reuse of existing Devbox pin contract (`PiPackageName`, tmux pin, no untrusted remote script steps) for the `pi` package.

## API (v1)

- `GET /v1/software` — list catalog.
- Instance representation includes `software[]`.
- `POST /v1/instances` accepts optional `packages: string[]`.
- `POST /v1/instances/{id}/software/{package_id}/install` — enqueue install.
- `remove` may follow in a later slice; not required for v1.

Authz: owner-scoped, same as other instance mutations.

## UI

- Remove Launch Pi button, `launchPiAvailable` gating, and create-time “Devbox” as a kind.
- Create VPS: optional “Pi coding agent” → `packages: ["pi"]`.
- Instance page: **Software** section — catalog, Install, status/error.
- Terminal unchanged as the execution surface.

## Documentation

Update operators/development docs that describe Devbox-only Pi and Launch Pi visibility. Pi profile docs: install Pi package first, then apply profile; run via terminal.

## Rollout

1. Persist software state + catalog + install path (with `pi`).
2. API + UI Software panel; create checkbox.
3. Remove Launch Pi; stop creating `devbox` (hard cut).
4. Enable VPS protect/clone; drop Devbox-only gates.
5. Deprecate Devbox image alias requirement for Pi.

## Testing

- Catalog validation (pins exact; no untrusted remote script steps).
- Install success / idempotent re-install / verify failure → `failed`.
- Create with `packages: ["pi"]` after ready.
- UI: no Launch Pi; Software panel install path.
- Protect/clone allowed on VPS; `kind=devbox` rejected.

## Open follow-ups (explicitly later)

- Uninstall / package remove.
- Broader catalog beyond Pi.
- Sandbox + software (if ever desired).
