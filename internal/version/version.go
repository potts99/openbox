// SPDX-License-Identifier: AGPL-3.0-only

// Package version exposes the OpenBox build version to every executable.
package version

// Version is the semantic version for development builds. Release builds may
// replace it with -ldflags "-X github.com/openbox-dev/openbox/internal/version.Version=vX.Y.Z".
var Version = "v0.0.0-dev"
