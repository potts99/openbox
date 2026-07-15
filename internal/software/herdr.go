// SPDX-License-Identifier: AGPL-3.0-only

package software

// herdrPackage builds the curated Herdr multiplexer catalog entry from pinned
// GitHub release assets (exact version + per-arch sha256).
func herdrPackage() Package {
	pkg := Package{
		ID:          "herdr",
		Name:        "Herdr",
		Description: "Agent-aware terminal multiplexer. Run herdr from the instance terminal.",
		Pins: []Pin{{
			Manager: "github-release",
			Name:    "ogulcancelik/herdr",
			Version: "0.7.4",
			Assets: []ReleaseAsset{
				{
					Arch:     "x86_64",
					Filename: "herdr-linux-x86_64",
					SHA256:   "bc0fc02d4ba500f9cac2353a43e67fe036785ecca6eb55378e050fac3c103059",
				},
				{
					Arch:     "aarch64",
					Filename: "herdr-linux-aarch64",
					SHA256:   "544e0002de42806d1ab64ccdef3a7e7414f24717b0b6b022bc9e57d2eefd26a2",
				},
			},
		}},
		Verify: [][]string{{"herdr", "--version"}},
	}
	if err := pkg.Validate(); err != nil {
		panic("software catalog: invalid herdr package: " + err.Error())
	}
	return pkg
}
