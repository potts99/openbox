# Owner authentication security model

OpenBox v0.1 has one administrative owner. Authentication protects every v1
control-plane route except health, bootstrap status/consume, and session login.
Resource ownership is taken from the authenticated principal; clients cannot
select an owner in request data.

## Bootstrap

On a fresh database, `openboxd` creates a cryptographically random one-time
bootstrap secret and prints it to the local daemon log. Only its SHA-256 digest
is stored. The challenge expires after 20 minutes, is consumed in the same
database transaction that installs the first owner credential, and cannot be
used when an owner credential already exists. Restart the daemon to issue a new
challenge after an unused challenge expires.

Bootstrap and password login are accepted over direct TLS or a loopback
connection, including a trusted SSH tunnel terminating on loopback. Plain HTTP
from a non-loopback peer is rejected. OpenBox deliberately does not use
`Forwarded` or `X-Forwarded-For` to relax this rule; a future trusted-proxy mode
must make that trust boundary explicit.

## Passwords and login

Passwords are bounded to 12–1024 bytes and stored in the versioned PHC string
format with Argon2id. The v0.1 parameters are 19 MiB memory, two iterations,
parallelism one, a random 16-byte salt, and a 32-byte result. Verification uses
a constant-time comparison and upgrades a wholly weaker supported parameter set
after a successful login. Mixed or stronger parameters are never downgraded.

Interactive login and bootstrap consume attempts use a bounded in-memory
limiter keyed by client address. This limits both guessing and unbounded
attacker-controlled Argon2id work; restarting the daemon clears the limiter, so
upstream connection controls remain useful for exposed hosts. Bootstrap verifies
the one-time secret digest before hashing the password so rejected secrets do
not pay the Argon2id cost.

## Browser sessions and CSRF

Browser sessions use random opaque cookie values whose SHA-256 digests are the
only values stored in SQLite. Authentication issues a fresh session rather than
accepting any pre-authentication identifier. Cookies are host-only, `HttpOnly`,
`SameSite=Strict`, and `Secure` when the daemon itself terminates TLS
(`r.TLS != nil`). Sessions have an absolute expiry. Restoring a browser session
rotates both the opaque session identifier and CSRF token in one database
transaction, allowing reload continuity without browser storage; the previous
values stop working immediately. Logout revokes the server-side record before
clearing the cookie.

If TLS terminates on a reverse proxy in front of a loopback HTTP listener, the
daemon does not see TLS and therefore issues `Secure=false` cookies. That
deployment is unsupported in v0.1: terminate TLS on `openboxd`, or keep the
listener on loopback and browse only through a tunnel that presents a local
HTTP origin. A future trusted-proxy mode must make `Secure` and forwarded-proto
trust explicit before non-loopback proxy deployments are recommended.

Every cookie-authenticated state-changing request must include the unpredictable
per-session CSRF value in `X-CSRF-Token`. The server stores only its digest and
performs a constant-time comparison. Bearer-token requests do not use cookies
and therefore do not require this CSRF header.

## API tokens

Owner API tokens have an explicit `owner` scope and may have an expiry. A token
secret is returned only by the create response. Listings expose metadata, never
the secret or stored digest. Revocation is persisted immediately; the next use
of the token is rejected. The CLI reads a token from `--token` or
`OPENBOX_TOKEN`; avoid putting a token directly in shell history when the
environment or another secret-injection mechanism is available.

## SSH public keys

OpenBox parses authorized keys with Go's official SSH parser, stores a canonical
public-key encoding, and uses the OpenSSH-style SHA-256 fingerprint. Malformed,
multi-key, option-bearing, and duplicate owner keys are rejected. Private keys
are never accepted by this API.

## Primary security references

The implementation choices above were checked against these primary sources on
15 July 2026:

- [OWASP Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html) — Argon2id work factors, unique salts, and upgrading work factors after authentication.
- [OWASP Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html) — session renewal, TLS, and `Secure`, `HttpOnly`, and `SameSite` cookie attributes.
- [OWASP Cross-Site Request Forgery Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html) — server-generated per-session synchronizer tokens and custom request headers.
- [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html) — login throttling and generic authentication failure handling.
- [OWASP Forgot Password Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Forgot_Password_Cheat_Sheet.html) — random, securely stored, expiring, single-use secrets.
- [Go `golang.org/x/crypto/argon2` documentation](https://pkg.go.dev/golang.org/x/crypto/argon2) — Argon2id API and encoded algorithm version.
- [Go `golang.org/x/crypto/ssh` documentation](https://pkg.go.dev/golang.org/x/crypto/ssh) — `ParseAuthorizedKey`, canonical marshaling, and `FingerprintSHA256`.
