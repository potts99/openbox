// SPDX-License-Identifier: AGPL-3.0-only

package software

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ReleaseAsset is one architecture-specific binary for a github-release pin.
type ReleaseAsset struct {
	Arch     string // "x86_64" or "aarch64"
	Filename string
	SHA256   string // lowercase hex, no sha256: prefix
}

// ReleaseFetcher downloads a release asset body on the host.
type ReleaseFetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

const maxReleaseBytes = 64 << 20

var defaultReleaseFetcher ReleaseFetcher = httpReleaseFetcher{
	client: &http.Client{Timeout: 2 * time.Minute},
	limit:  maxReleaseBytes,
}

// SetReleaseFetcherForTest overrides the default release fetcher; restore with the returned func.
func SetReleaseFetcherForTest(f ReleaseFetcher) func() {
	prev := defaultReleaseFetcher
	defaultReleaseFetcher = f
	return func() { defaultReleaseFetcher = prev }
}

// ReleaseURL builds the canonical GitHub release asset URL.
func ReleaseURL(repo, version, filename string) string {
	return "https://github.com/" + repo + "/releases/download/v" + version + "/" + filename
}

type httpReleaseFetcher struct {
	client *http.Client
	limit  int64
}

func (f httpReleaseFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, f.limit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > f.limit {
		return nil, fmt.Errorf("download %s: exceeds size limit", url)
	}
	return body, nil
}

func validateGitHubReleasePin(pin Pin) error {
	if err := validateRepoName(pin.Name); err != nil {
		return err
	}
	if pin.Version == "" {
		return fmt.Errorf("package %q is missing a version", pin.Name)
	}
	if !exactVersion(pin.Version) {
		return fmt.Errorf("package %q version %q is not exact", pin.Name, pin.Version)
	}
	required := map[string]bool{"x86_64": false, "aarch64": false}
	for _, asset := range pin.Assets {
		if asset.Arch != "x86_64" && asset.Arch != "aarch64" {
			return fmt.Errorf("package %q has unsupported arch %q", pin.Name, asset.Arch)
		}
		if asset.Filename == "" {
			return fmt.Errorf("package %q arch %q is missing a filename", pin.Name, asset.Arch)
		}
		if !isSHA256Hex(asset.SHA256) {
			return fmt.Errorf("package %q arch %q has invalid sha256", pin.Name, asset.Arch)
		}
		required[asset.Arch] = true
	}
	for arch, present := range required {
		if !present {
			return fmt.Errorf("package %q is missing %s release asset", pin.Name, arch)
		}
	}
	return nil
}

func validateRepoName(name string) error {
	if name == "" {
		return fmt.Errorf("package pin is missing a name")
	}
	owner, repo, ok := strings.Cut(name, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") || strings.ContainsAny(name, " \t") {
		return fmt.Errorf("package %q must be owner/repo", name)
	}
	return nil
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func hasGitHubReleasePin(pins []Pin) bool {
	for _, pin := range pins {
		if pin.Manager == "github-release" {
			return true
		}
	}
	return false
}

func githubReleasePin(pkg Package) (Pin, bool) {
	for _, pin := range pkg.Pins {
		if pin.Manager == "github-release" {
			return pin, true
		}
	}
	return Pin{}, false
}

func assetForArch(pin Pin, arch string) (ReleaseAsset, error) {
	for _, asset := range pin.Assets {
		if asset.Arch == arch {
			return asset, nil
		}
	}
	return ReleaseAsset{}, fmt.Errorf("unsupported architecture %q", arch)
}

func verifySHA256(body []byte, wantHex string) error {
	if len(body) == 0 {
		return fmt.Errorf("empty release body")
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(got), []byte(wantHex)) != 1 {
		return fmt.Errorf("sha256 mismatch: got %s want %s", got, wantHex)
	}
	return nil
}
