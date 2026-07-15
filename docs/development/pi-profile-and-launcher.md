# Pi profile and software catalog

| Package / path | Role |
|---|---|
| `images/devbox/` | Pinned Pi + tmux definition (pin source for the catalog) |
| `internal/software` | Curated catalog + guest install recipes (`pi` first) |
| `internal/images` | Curated image manifests; Devbox alias retained as legacy pin carrier |
| `internal/profiles/pi` | Versioned owner Pi profiles, materialize, project separation |
| `internal/pi` | tmux/Pi argv helpers for terminal sessions |
| `web/src/pages/PiProfile.tsx` | Preview, history, apply, rollback |
| `web/src/pages/InstancePage.tsx` | Software Install panel (no Launch Pi) |

## Profile apply

Apply writes `/root/.pi/agent/settings.json` atomically (temp + rename) inside
each selected instance via Incus recorded `Exec`. OpenBox never writes
`trust.json` or project-local `.pi/`. Install catalog package `pi` before apply
when the guest does not already have the CLI.

## Running Pi

Use the instance Terminal. A typical persistent session:

```text
tmux new-session -A -s pi [-c <workdir>] -- pi
```

Detach leaves the tmux session running. Pi session files and local unsupported
product logins stay under `~/.pi/agent/` on the VPS disk across stop/start.
