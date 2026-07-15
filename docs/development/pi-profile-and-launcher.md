# Pi profile and Launch Pi

Slice 12 packages:

| Package / path | Role |
|---|---|
| `images/devbox/` | Pinned Pi + tmux Devbox image definition |
| `internal/images` | Loads pins into curated Devbox manifests |
| `internal/profiles/pi` | Versioned owner Pi profiles, materialize, project separation |
| `internal/pi` | Launch Pi tmux argv + Pi-enabled policy |
| `web/src/pages/PiProfile.tsx` | Preview, history, apply, rollback |
| `web/src/components/LaunchPi.tsx` | Dashboard Launch Pi control |

## Profile apply

Apply writes `~/.pi/agent/settings.json` atomically (temp + rename) inside each
selected instance. OpenBox never writes `trust.json` or project-local `.pi/`.

## Launch Pi

Browser open with `session_name=pi` runs:

```text
tmux new-session -A -s pi [-c <workdir>] -- pi
```

Detach leaves the tmux session running. Pi session files and local unsupported
product logins stay under `~/.pi/agent/` on the Devbox disk across stop/start.
