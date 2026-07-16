# Egress allowlists and network UX — design (Slice 19)

**Status:** Approved for implementation (Approach 1: profile service + apply orchestration)  
**Date:** 2026-07-16  
**Surface:** egress profiles API, instance attach/switch, `dnsproxy` apply path, CLI `network`, dashboard Network policy page  
**Plan:** [docs/plans/19-egress-allowlists-and-network-ux.md](../../plans/19-egress-allowlists-and-network-ux.md)  
**Depends on:** Slice 14 (egress modes, baseline ACLs, fail-closed verify)

## Goal

Make restricted egress useful and operable: administrators manage **system egress profiles** (mode + allowlist); OpenBox resolves hostnames on the host, programs per-instance restricted ACLs, and exposes effective policy in API, CLI, and dashboard — without letting guests relax containment.

## Non-goals

- User-authored eBPF programs
- Transparent TLS interception
- Cross-host / multi-node network policy
- Slice 15 LLM Gateway credential work (keep existing management placeholder destination only)
- Per-owner private profile catalogs (profiles are host-global in this slice)
- Packet or DNS payload logging in inspect/UI

## Problem

Slice 14 shipped `standard` / `restricted` modes, default-deny east-west ACLs, fail-closed NIC verify, and inspect telemetry. Restricted instances still start with an **empty** allowlist on the apply path: `EnsureRestrictedACL` and `dnsproxy` exist but are unused, resolution state stays `idle`, and there is no profile CRUD or dedicated network UX. Operators cannot approve destinations without widening peer/host access.

## Decisions

| Topic | Choice |
|-------|--------|
| Allowlist ownership | **Profile-linked only** (no per-instance destination lists) |
| Profile scope | **System / host-global**, editable |
| Default attach | If create omits profile: sandbox → seeded `restricted`, VPS/devbox → seeded `standard` |
| Mode authority | **Profile** owns `egress_mode`; instance inherits |
| Profile edit | Persist, then **immediately re-apply** to all attached running instances |
| Fan-out failure | **Keep** the saved profile; mark failed instances error / not-ready; continue other instances |
| Profile switch | Allowed anytime on a live instance; immediate re-apply |
| Profile delete | **Rejected** while any instance references it; seeded defaults never deletable |
| DNS | Host-side `dnsproxy.AllowlistResolver` on apply + TTL refresh; rebinding fail-closed |
| Architecture | Small profiles app/service + orchestration; Incus adapter stays ACL plumbing |

## Data model

### `egress_profiles` (migrate from owner-scoped schema)

Current table is owner-scoped (`owner_id`, `UNIQUE(owner_id, name)`). Slice 19 migrates to host-global profiles:

| Column | Notes |
|--------|-------|
| `id` | Stable ID (TEXT PK) |
| `name` | Unique globally; seeded `standard`, `restricted` |
| `mode` | `standard` \| `restricted` |
| `allowed_destinations_json` | JSON array of IP, CIDR, and/or exact hostnames |
| `dns_policy` | Retain column; set constant (e.g. `host_resolve`) — unused for guest control |
| `system` | Boolean: seeded defaults (`true`) cannot be deleted |
| `created_at`, `updated_at` | Timestamps |

Drop `owner_id` (or leave nullable unused — prefer drop for clarity). Repository gains full CRUD + seed-on-boot.

### `instances`

| Change | Notes |
|--------|-------|
| `egress_profile_id` | NOT NULL FK → `egress_profiles(id)` |
| `egress_mode` | Kept as denormalized cache of attached profile mode for queries/inspect; **updated whenever profile attach or profile mode changes** — never independently authoritative |

Backfill: existing sandboxes → `restricted` profile; other kinds → `standard` profile.

### Allowlist entry rules

Reuse / extend existing parsers in `internal/networkpolicy`:

- IPs and CIDRs via `ParseAllowedDestinations`
- Exact hostnames via `ParseAllowlistHostnames` (no wildcards)
- Mixed arrays allowed in one profile list; apply path splits literals vs hostnames

## Architecture

```text
  API / CLI / Dashboard
            │
            ▼
  ┌─────────────────────┐
  │ Egress profile svc  │  CRUD, seed, delete guards
  └──────────┬──────────┘
             │ attach / edit / switch
             ▼
  ┌─────────────────────┐
  │ Instances service   │  create defaults, fan-out re-apply
  └──────────┬──────────┘
             │
     ┌───────┴────────┐
     ▼                ▼
 dnsproxy         Incus adapter
 (resolve/TTL)    EnsureRestrictedACL
                  NICACLs + Verify
```

### Seeded profiles

On daemon start (or first migration):

1. Ensure profile `standard` — mode `standard`, empty allowlist, `system=true`
2. Ensure profile `restricted` — mode `restricted`, empty allowlist, `system=true`

Empty restricted allowlist remains valid: baseline `openbox-default-deny` still permits DNS and LLM gateway placeholder only.

### Apply path

Triggered by: instance create (after runtime start), recovery/reconcile, profile attach/switch, profile allowlist/mode edit (fan-out), and TTL refresh hooks.

For each instance:

1. Load attached profile (`mode`, destinations).
2. Sync denormalized `instance.egress_mode` from profile.
3. Split destinations into literals vs hostnames.
4. Resolve hostnames with `dnsproxy.AllowlistResolver` (rebinding filter, min/max TTL).
5. Merge safe addresses with literal IPs/CIDRs.
6. **Restricted:** `EnsureRestrictedACL(RestrictedACLName(instanceID), destinations)` then NIC stack `[openbox-default-deny, <restricted-ACL>]`.
7. **Standard:** NIC stack `[openbox-default-deny, openbox-egress-standard]` (no per-instance allowlist ACL required; profile allowlist may exist but does not narrow `0.0.0.0/0`).
8. `VerifyNetworkPolicy`; on failure → fail closed (instance error / not ready), increment `denied_flows`, set resolution `failed` with **hostname names only**.
9. Update `network_policy.resolution` to `pending` / `resolved` / `failed` / `idle` (idle when no hostnames).

### Profile edit fan-out

1. Persist profile update in SQLite.
2. List instances with `egress_profile_id = profile.id`.
3. For each instance that has a `RuntimeRef` (started or recoverable), run apply path; skip not-yet-provisioned rows.
4. Collect per-instance errors; do **not** roll back the profile row.
5. API response includes update result plus optional `apply_errors[]` (`instance_id`, message) so operators see partial failure.

### TTL refresh

- `AllowlistResolver` cache expiry triggers re-resolve without trusting guest DNS.
- If refresh yields no safe addresses → fail closed for that instance (same as apply failure).
- Hook into existing instance refresh / reconcile paths that already verify network policy; no guest-facing control plane.

### Remove path

On instance delete: `RemoveNetworkPolicy` deletes the per-instance restricted ACL (already implemented). Shared ACLs and egress profiles are retained.

## API

### Egress profiles

| Method | Path | Behavior |
|--------|------|----------|
| `GET` | `/v1/network/egress-profiles` | List all system profiles |
| `POST` | `/v1/network/egress-profiles` | Create custom profile (`name`, `mode`, `allowed_destinations`) |
| `GET` | `/v1/network/egress-profiles/{id}` | Show one profile (+ attached instance count) |
| `PATCH` | `/v1/network/egress-profiles/{id}` | Update name (non-system), mode, and/or destinations → fan-out re-apply |
| `DELETE` | `/v1/network/egress-profiles/{id}` | Delete if not system and reference count is 0 |

### Instances

| Change | Behavior |
|--------|----------|
| Create | Optional `egress_profile_id`; else kind → seeded default |
| Attach | `PUT` or `POST` `/v1/instances/{id}/network/egress-profile` with `{ "egress_profile_id": "..." }` → immediate re-apply |
| Inspect / GET | Include `egress_profile_id`, profile `name`, and existing `network_policy` with live resolution state |

OpenAPI schemas: `EgressProfile`, `AllowedDestination` list as string array, extend `Instance` / create request.

## CLI

- `openbox network profiles ls|show|create|edit|delete`
- `openbox network attach <instance-id> <profile-id-or-name>`
- `openbox inspect` shows profile name/id, mode, ACLs, resolution, denied flows (no resolved IPs)

## Dashboard

- **Network policy** page: list profiles, create/edit allowlist and mode, show attached count, delete when allowed.
- **Instance detail**: profile name, effective mode, ACL names, resolution state, denied flows; control to switch profile; link into profile editor.
- Follow existing console visual language (not a new marketing surface).

## Error handling

| Case | Behavior |
|------|----------|
| Unknown / invalid destination entry | Reject profile write (400) |
| Hostname resolves only to private/rebinding ranges | Fail closed on apply; resolution `failed` |
| Incus ACL ensure/verify mismatch | Fail closed; `denied_flows++` |
| Delete in-use or system profile | 409 / failed precondition |
| Fan-out partial failure | 200/OK on profile save with `apply_errors`; instances in error until fixed |

## Testing

### Required (always)

- Profile seed, CRUD validation, delete guards
- Create without profile id attaches defaults by kind
- Restricted empty allowlist → NIC `[default-deny]` or `[default-deny, empty-restricted-ACL]` consistent with implementation choice below
- Restricted with IP + hostname → resolve + `EnsureRestrictedACL` + NIC stack includes restricted ACL
- Rebinding hostname blocks readiness
- Profile edit fan-out re-applies; keeps profile on instance apply failure
- Attach/switch updates mode + ACLs
- HTTP/CLI/UI contract tests for profile fields and resolution states

**Empty restricted ACL:** Prefer ensuring a named restricted ACL even when destination list is empty and stacking it in `NICACLs`, so create/delete paths stay symmetric with `RemoveNetworkPolicy`. Baseline DNS/LLM rules remain on `openbox-default-deny`.

### Opt-in live Incus

When `OPENBOX_INCUS_TEST_SOCKET` (and existing storage/image envs) are set: connectivity matrix for host, peer, DNS, internet, LLM placeholder, and allowlist destination — container and VM. Otherwise skip.

## Documentation

- Update `docs/security/networking.md` for profiles, apply/refresh, fan-out semantics
- Add `docs/operators/` workflow: approve destinations, attach profiles, interpret fail-closed / `apply_errors`
- Keep Slice 19 plan checkboxes in sync when tasks land

## File touch map (expected)

| Area | Paths |
|------|--------|
| Domain / persistence | `internal/domain/`, migrations, `internal/persistence/sqlite/` |
| Policy / DNS | `internal/networkpolicy/`, `internal/dnsproxy/` (wire only) |
| App | new or extended profiles service; `internal/app/instances/` |
| Runtime | `internal/runtime/incus/acl.go` (call `EnsureRestrictedACL`, pass ACL name into `NICACLs`) |
| API | `api/openapi.yaml`, `internal/httpapi/` |
| CLI | `cmd/openbox/network.go` (+ command registration) |
| Web | `web/src/pages/NetworkPolicy.tsx`, routing, `InstancePage`, client types |
| Docs | `docs/security/networking.md`, `docs/operators/…`, plan 19 |

## Acceptance gate

- Administrators can grant and revoke restricted destinations via profiles without widening peer or host access
- Host-side DNS resolution runs on the apply path; guests cannot relax allowlists
- Operators can list/edit profiles and see effective policy in CLI and dashboard without packet/DNS payload logs
- Empty restricted profile still cannot reach arbitrary internet or peers
- Opt-in live Incus matrix passes when configured

## Open implementation notes

- Exact HTTP status for fan-out partial success: prefer `200` with `apply_errors` over multi-status unless OpenAPI patterns elsewhere dictate otherwise
- Profile rename: allowed for non-system profiles; IDs remain stable for instance FKs
- Standard profile allowlist is stored and editable but does not restrict internet egress while mode is `standard`
