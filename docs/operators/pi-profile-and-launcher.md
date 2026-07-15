# Pi profiles and Launch Pi

## Shared profile

OpenBox stores an owner-level **Pi profile** (settings, packages, extensions,
skills, prompts, themes, model preferences). Updates create a new version;
history supports preview and rollback. Apply copies the current settings into
selected instances at `~/.pi/agent/settings.json`.

Credentials and gateway secrets are not part of the profile (see LLM Gateway
slices). Clean Devbox bases must not contain personal Pi authentication:
prepare bases without signing into providers, then clone.

## Launch Pi

On Pi-enabled Devboxes and Sandboxes, **Launch Pi** opens the browser terminal
attached to a named `tmux` session running `pi`. Closing the browser does not
stop Pi. Plain VPS images do not show Launch Pi.

## Clean bases

When protecting a Devbox as a reusable base:

1. Do not store personal API keys or product-subscription logins in the base.
2. Prefer applying the shared OpenBox Pi profile after clone.
3. Expect a secrets warning if you clone a personal Devbox that may already
   contain guest files under `~/.pi/agent/auth`.

## Apply note

Guest apply uses Incus recorded `Exec` to write `~/.pi/agent/settings.json`
atomically (mkdir, temp write, rename) into each selected instance. OpenBox
instances currently run as root over SSH, so the guest path is
`/root/.pi/agent/settings.json`.
