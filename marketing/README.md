# OpenBox marketing site

Standalone static landing page — **not** part of the embedded owner console.

| Path | Role |
|------|------|
| `marketing/` | Public marketing site (this directory) |
| `web/` | Owner console SPA, embedded in `openboxd` |
| `app.<host>/` | Console (API + dashboard) |
| apex / `www` | Marketing (optional Caddy site) |

## Local preview

```sh
cd marketing
python3 -m http.server 4173
# open http://127.0.0.1:4173/
```

## Deploy

Copy the directory to the host and serve it with Caddy as a **separate site**
from the console. See [`deploy/caddy/README.md`](../deploy/caddy/README.md).

```sh
rsync -a --delete ./marketing/ ubuntu@host:/var/www/openbox-marketing/
```

Console CTA links default to `https://app.kindling.systems/`. Edit
`index.html` if your console hostname differs.

## Claims

Keep copy honest: no cold-start numbers, proxy-secret injection, or managed SLA.
