// SPDX-License-Identifier: AGPL-3.0-only

// Package devbox carries the checked-in OpenBox Devbox image definition:
// pinned package versions and the install/verify recipe metadata that the
// image build consumes. It holds no build logic; see internal/images for the
// loader and validator.
package devbox

import _ "embed"

// Definition is the raw Devbox image definition. Parse and validate it with
// images.LoadDevboxDefinition.
//
//go:embed devbox.json
var Definition []byte
