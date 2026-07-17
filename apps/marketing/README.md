# OpenBox marketing site

Public product marketing site for Cloudflare Pages (or equivalent CDN).
This is **not** part of self-hosted OpenBox and is **not** served by `openboxd`
or the operator Caddy gateway.

| App | Path | Deploys to |
|-----|------|------------|
| Marketing | `apps/marketing` | Cloudflare Pages |
| Console | `apps/web` | Embedded in `openboxd` |

## Cloudflare Pages

- **Root directory:** `apps/marketing`
- **Build command:** *(empty — static files)*
- **Output directory:** `/`

## Local preview

```sh
cd apps/marketing
python3 -m http.server 4173
# open http://127.0.0.1:4173/
```

Primary CTAs point at the public GitHub repo (self-host / source). There is
no managed OpenBox cloud yet — do not link the marketing site at a hosted
console hostname.

## Claims

Keep copy honest: no cold-start numbers, proxy-secret injection, or managed SLA.
