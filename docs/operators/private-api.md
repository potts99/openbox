# Private API and CLI operation

OpenBox v0.1 currently exposes its control API only on a loopback address. This
is a deliberate safety boundary while owner tokens and browser sessions remain
scheduled for the next implementation slice.

## Starting the daemon

The relevant defaults are:

```text
openboxd \
  --api-address 127.0.0.1:8443 \
  --owner-id owner-local \
  --owner-name "Local owner"
```

`openboxd` refuses a non-loopback API address. It creates the configured local
owner on first start and refuses to silently rename an existing owner ID.

The loopback listener uses HTTP by default. It is intended for same-host use or
a trusted SSH tunnel. To serve HTTPS on loopback, provide both
`--api-tls-cert` and `--api-tls-key`; configuring only one is an error. The
daemon requires TLS 1.3 when these options are used. The certificate must be
trusted by the CLI host (or supplied through a custom HTTP client when using
the Go client package).

Do not publish or reverse-proxy this pre-authentication API to an untrusted
network. Public and multi-session access belongs to the owner-authentication
slice.

## Using the CLI

The CLI defaults to `http://127.0.0.1:8443`. Override it per command or through
the environment:

```text
openbox ls --server http://127.0.0.1:8443
OPENBOX_SERVER=https://127.0.0.1:8443 openbox doctor
```

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
