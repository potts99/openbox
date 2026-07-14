// SPDX-License-Identifier: AGPL-3.0-only

// Package migrations owns the forward-only embedded SQLite schema migrations.
package migrations

import "embed"

// Files contains immutable, checksum-verified migration sources.
//
//go:embed *.sql
var Files embed.FS
