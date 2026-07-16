# Egress profiles (operators)

System egress profiles control outbound network access for OpenBox instances.
Profiles are host-global: every owner on the host shares the same catalog.

## Seeded defaults

On daemon start OpenBox ensures two system profiles:

| Name | Mode | Allowlist | Notes |
|------|------|-----------|--------|
| `standard` | `standard` | empty | Public internet egress via shared ACL |
| `restricted` | `restricted` | empty | DNS + LLM gateway placeholder only until destinations are approved |

System profiles cannot be deleted. Their allowlists (and mode) are editable.

## Approve destinations

1. Create or edit a restricted profile:
   - CLI: `openbox network profiles create allow-npm --mode restricted --allow registry.npmjs.org`
   - CLI: `openbox network profiles edit allow-npm --allow registry.npmjs.org,203.0.113.10`
   - Dashboard: **Network policy** → select profile → edit destinations (one per line) → **Save and re-apply**
2. Attach instances:
   - `openbox network attach INSTANCE allow-npm`
   - Or set `egress_profile_id` at create time
3. Inspect effective policy:
   - `openbox inspect INSTANCE` (profile id, mode, ACLs, resolution state, denied flows)
   - Dashboard instance detail shows the same fields

Allowed entries are IP addresses, CIDRs, and exact hostnames. Wildcards are rejected.
Hostname resolution runs on the host with rebinding protection; resolved IPs are never shown in API/CLI/UI.

## Fail-closed behavior

- If hostname resolution fails or returns only private/rebinding addresses, apply fails and the instance is not reported ready (or is marked error on re-apply).
- If Incus ACL programming drifts from the intended stack, apply/verify fails closed.
- Editing a profile saves first, then re-applies to attached instances with a `RuntimeRef`. Partial failures return `apply_errors` without rolling back the saved profile.

## Delete rules

- Delete is rejected for system profiles.
- Delete is rejected while any non-tombstoned instance still references the profile.
