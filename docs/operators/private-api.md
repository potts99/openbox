# Private API and CLI operation

OpenBox v0.1 serves its owner console and control API on the same listener. The
default remains loopback-only. Browser sessions and owner-scoped API tokens
protect the control plane after one-time setup.

## Starting and claiming the daemon

The relevant defaults are:

```text
openboxd \
  --api-address 127.0.0.1:8443 \
  --owner-id owner-local \
  --owner-name "Local owner"
```

On first start, `openboxd` creates the configured owner record and prints a
one-time bootstrap secret. Open the dashboard at the listener address and use
that secret within 20 minutes to install the owner password. Only a digest of
the secret is stored, and the database transaction can succeed only once.
Restart the daemon to issue a replacement after an unused challenge expires.
Pass `--storage-pool` so the daemon also bootstraps the managed Incus project,
bridge, and profiles on startup.

The loopback listener uses HTTP by default. Password setup and login over HTTP
are accepted only from a direct loopback peer, including the server end of a
trusted SSH tunnel. To serve HTTPS, provide both `--api-tls-cert` and
`--api-tls-key`; configuring only one is an error. The daemon requires TLS 1.3
and refuses a non-loopback listener without TLS. The certificate must be
trusted by the browser and CLI host. Session cookies are marked `Secure` only
when `openboxd` itself terminates TLS; TLS-terminating reverse proxies in front
of a loopback HTTP listener are unsupported in v0.1.

Forwarding headers do not make an insecure remote request trusted. Terminate a
trusted tunnel on loopback or connect directly with TLS. Keep the listener
behind host firewall policy even though health, bootstrap, and login are the
only unauthenticated routes.

## Using the CLI

The CLI defaults to `http://127.0.0.1:8443` and requires an owner API token for
control-plane commands. Create an owner-scoped token in an authenticated owner
session; its secret is shown once. Prefer `OPENBOX_TOKEN` so the secret does not
appear directly in shell history:

```text
OPENBOX_TOKEN="$OPENBOX_TOKEN" openbox ls
OPENBOX_SERVER=https://openbox.example OPENBOX_TOKEN="$OPENBOX_TOKEN" openbox doctor
```

`--token` is available for controlled automation environments. Token listings
contain only identifiers, names, scopes, timestamps, and revocation state.
Revoking a token takes effect on its next request.

`openbox new` accepts an SSH public key with `--ssh-key`. The value may be a
public-key string or file path. If omitted, the CLI checks
`OPENBOX_SSH_PUBLIC_KEY` and then common public keys under `~/.ssh`.

Every lifecycle mutation uses an idempotency key. The CLI generates one unless
`--idempotency-key` is supplied. Save an explicitly supplied key when retrying
automation after a lost response.

## Watching and canceling operations

`openbox operation watch OPERATION_ID` reconnects with the last durable event
sequence and exits on terminal success or failure. Disconnecting the CLI does
not cancel the operation.

API cancellation is available only while an operation is pending, unclaimed,
and still at its initial `runtime` stage. A `cancellation_unsafe` response means
the worker may already have started external work; inspect or watch the
operation instead of assuming rollback.

## Shutdown behavior

On SIGINT or SIGTERM, the daemon stops accepting API work, allows a bounded
HTTP shutdown, stops the local operation/reconciliation loops, and then closes
SQLite. Existing Incus instances continue running independently.
