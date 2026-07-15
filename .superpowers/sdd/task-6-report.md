# Task 6 report — Apply policy before ready; remove after runtime deletion

## RED

- Added lifecycle tests for policy application before readiness, sandbox restricted defaults and persistence, fail-closed application errors, recovery create, and policy removal after confirmed runtime deletion.
- The initial focused run failed because `Options.NetworkPolicy` and `domain.Instance.EgressMode` did not exist:

  ```text
  unknown field NetworkPolicy in struct literal of type Options
  created.EgressMode undefined
  ```

- Added the Incus NIC-ACL test. Its initial focused run failed because `Adapter.ApplyNetworkPolicy` did not exist.

## GREEN

- Added persisted per-instance `egress_mode` through migration `008_instance_egress_mode.sql`; sandbox defaults to `restricted`, while VPS and Devbox default to `standard`.
- Added an injected narrow `NetworkPolicy` port. Create and recovery apply it after start and before VM readiness/refresh; errors mark the instance failed and prevent readiness.
- Wired the Incus adapter in `openboxd`, which PATCHes `eth0.security.acls` using `NICACLs`. Restricted empty allowlists retain only the baseline ACL.
- Delete confirms the runtime is absent, then removes the per-instance restricted ACL (not-found is ignored) before metadata finalization. Shared ACLs remain untouched.

## Verification

```text
go test ./internal/app/instances ./internal/persistence/sqlite ./internal/runtime/incus ./cmd/openboxd -count=1
go test ./...
```

Both passed.
