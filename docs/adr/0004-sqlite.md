# ADR 0004: SQLite for local metadata

- Status: Accepted
- Date: 2026-07-14

## Context

OpenBox v0.1 is a single-server, single-owner system and should not require an external database.

## Decision

Use SQLite for desired state and metadata, with WAL mode, foreign keys, explicit migrations, UTC timestamps, and transactional operation creation. The database schema begins in slice 01.

## Consequences

Installation and backup stay simple. Multi-host work must revisit write coordination rather than stretching this decision into a distributed database design.
