# ADR 0003: OpenAPI owns the public API contract

- Status: Accepted
- Date: 2026-07-14

## Context

The CLI, dashboard, SSH surface, and future SDKs must share one versioned contract.

## Decision

Starting with the API slice, a repository-owned OpenAPI document will be the canonical HTTP contract. Generated clients and server types are derived artifacts and must pass generated-file drift checks. Domain behavior remains handwritten behind generated transport types.

## Consequences

API changes begin in the contract and receive compatibility review. No OpenAPI document or generator is added in the foundation slice because it has no product endpoints.
