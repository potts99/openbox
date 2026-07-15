# Pi profiles and software catalog

Install the curated `pi` software catalog package before applying profiles.
See [Software catalog](software-catalog.md) for `pi` and `herdr`.

## Shared profile

OpenBox stores an owner-level **Pi profile** (settings, packages, extensions,
skills, prompts, themes, model preferences). Updates create a new version;
history supports preview and rollback. Apply copies the current settings into
selected instances at `~/.pi/agent/settings.json`.

Credentials and gateway secrets are not part of the profile (see LLM Gateway
slices). Clean VPS bases must not contain personal Pi authentication:
prepare bases without signing into providers, then clone.

## Software catalog (Pi)

Persistent instances are **VPS** (Sandbox stays disposable). Install curated
packages from the software catalog at create time (`packages`) or later from
the instance Software panel / API. **Pi** (pinned CLI + tmux) is the first
package.

Run Pi from the normal **Terminal** (for example `tmux new-session -A -s pi -- pi`).
There is no separate Launch Pi control.

## Clean bases

When protecting a VPS as a reusable base:

1. Do not store personal API keys or product-subscription logins in the base.
2. Prefer applying the shared OpenBox Pi profile after clone.
3. Expect a secrets warning if you clone an unprotected VPS that already has
   Pi installed (guest files under `~/.pi/agent/auth` may be copied).

## Apply note

Guest apply uses Incus recorded `Exec` to write `~/.pi/agent/settings.json`
atomically (mkdir, temp write, rename) into each selected instance. OpenBox
instances currently run as root over SSH, so the guest path is
`/root/.pi/agent/settings.json`. Install the Pi software package before
relying on profile apply.
