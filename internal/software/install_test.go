// SPDX-License-Identifier: AGPL-3.0-only

package software_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"testing"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/software"
)

type recordingExecer struct {
	commands [][]string
	stdins   [][]byte
	results  map[string]runtimeapi.ExecResult
	errFor   map[string]error
}

func (e *recordingExecer) Exec(_ context.Context, req runtimeapi.ExecRequest) (runtimeapi.ExecResult, error) {
	e.commands = append(e.commands, append([]string{}, req.Command...))
	if req.Stdin != nil {
		b, _ := io.ReadAll(req.Stdin)
		e.stdins = append(e.stdins, b)
	} else {
		e.stdins = append(e.stdins, nil)
	}
	key := strings.Join(req.Command, " ")
	if err, ok := e.errFor[key]; ok {
		return runtimeapi.ExecResult{}, err
	}
	if res, ok := e.results[key]; ok {
		return res, nil
	}
	return runtimeapi.ExecResult{ExitCode: 0}, nil
}

type mapFetcher map[string][]byte

func (m mapFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	body, ok := m[url]
	if !ok {
		return nil, fmt.Errorf("missing %s", url)
	}
	return body, nil
}

func TestInstallRunsPinsThenVerify(t *testing.T) {
	t.Parallel()
	pkg, ok := software.DefaultCatalog().Get("pi")
	if !ok {
		t.Fatal("missing pi")
	}
	execer := &recordingExecer{}
	if err := software.Install(context.Background(), execer, "ref-1", pkg, software.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(execer.commands) != len(pkg.Install)+len(pkg.Verify) {
		t.Fatalf("commands=%d want %d", len(execer.commands), len(pkg.Install)+len(pkg.Verify))
	}
	last := execer.commands[len(execer.commands)-1]
	if strings.Join(last, " ") != "tmux -V" {
		t.Fatalf("last command=%v", last)
	}
	firstVerify := execer.commands[len(pkg.Install)]
	if strings.Join(firstVerify, " ") != "pi --version" {
		t.Fatalf("first verify=%v", firstVerify)
	}
}

func TestInstallFailsOnVerify(t *testing.T) {
	t.Parallel()
	pkg := software.Package{
		ID:      "pi",
		Name:    "Pi",
		Install: [][]string{{"true"}},
		Verify:  [][]string{{"pi", "--version"}},
	}
	execer := &recordingExecer{
		results: map[string]runtimeapi.ExecResult{
			"pi --version": {ExitCode: 1, Stderr: []byte("not found")},
		},
	}
	err := software.Install(context.Background(), execer, "ref-1", pkg, software.InstallOptions{})
	if err == nil {
		t.Fatal("expected verify failure")
	}
	if !strings.Contains(err.Error(), "verify") {
		t.Fatalf("error=%v", err)
	}
}

func TestInstallFailsOnInstallStep(t *testing.T) {
	t.Parallel()
	pkg := software.Package{
		ID:      "pi",
		Name:    "Pi",
		Install: [][]string{{"apt-get", "update"}},
		Verify:  [][]string{{"true"}},
	}
	execer := &recordingExecer{
		results: map[string]runtimeapi.ExecResult{
			"apt-get update": {ExitCode: 100, Stderr: []byte("fail")},
		},
	}
	err := software.Install(context.Background(), execer, "ref-1", pkg, software.InstallOptions{})
	if err == nil {
		t.Fatal("expected install failure")
	}
}

func testReleasePackage(x86Digest, aarchDigest string) software.Package {
	return software.Package{
		ID:   "herdr",
		Name: "Herdr",
		Pins: []software.Pin{{
			Manager: "github-release",
			Name:    "ogulcancelik/herdr",
			Version: "0.7.4",
			Assets: []software.ReleaseAsset{
				{Arch: "x86_64", Filename: "herdr-linux-x86_64", SHA256: x86Digest},
				{Arch: "aarch64", Filename: "herdr-linux-aarch64", SHA256: aarchDigest},
			},
		}},
		Verify: [][]string{{"herdr", "--version"}},
	}
}

func TestInstallGitHubReleaseWritesBinaryAndVerifies(t *testing.T) {
	t.Parallel()
	body := []byte("herdr-bytes")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	pkg := testReleasePackage(digest, strings.Repeat("b", 64))
	url := software.ReleaseURL("ogulcancelik/herdr", "0.7.4", "herdr-linux-x86_64")
	execer := &recordingExecer{}
	err := software.Install(context.Background(), execer, "ref-1", pkg, software.InstallOptions{
		Architecture: "x86_64",
		Fetcher:      mapFetcher{url: body},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(execer.commands) < 4 {
		t.Fatalf("commands=%v", execer.commands)
	}
	if strings.Join(execer.commands[0], " ") != "tee /usr/local/bin/herdr.openbox-tmp" {
		t.Fatalf("tee=%v", execer.commands[0])
	}
	if string(execer.stdins[0]) != "herdr-bytes" {
		t.Fatalf("stdin=%q", execer.stdins[0])
	}
	if strings.Join(execer.commands[1], " ") != "chmod 0755 /usr/local/bin/herdr.openbox-tmp" {
		t.Fatalf("chmod=%v", execer.commands[1])
	}
	if strings.Join(execer.commands[2], " ") != "mv /usr/local/bin/herdr.openbox-tmp /usr/local/bin/herdr" {
		t.Fatalf("mv=%v", execer.commands[2])
	}
	if strings.Join(execer.commands[3], " ") != "herdr --version" {
		t.Fatalf("verify=%v", execer.commands[3])
	}
}

func TestInstallGitHubReleaseRejectsDigestMismatch(t *testing.T) {
	t.Parallel()
	pkg := testReleasePackage(strings.Repeat("a", 64), strings.Repeat("b", 64))
	url := software.ReleaseURL("ogulcancelik/herdr", "0.7.4", "herdr-linux-x86_64")
	err := software.Install(context.Background(), &recordingExecer{}, "ref-1", pkg, software.InstallOptions{
		Architecture: "x86_64",
		Fetcher:      mapFetcher{url: []byte("herdr-bytes")},
	})
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("error=%v", err)
	}
}

func TestInstallGitHubReleaseRejectsUnsupportedArch(t *testing.T) {
	t.Parallel()
	pkg := testReleasePackage(strings.Repeat("a", 64), strings.Repeat("b", 64))
	err := software.Install(context.Background(), &recordingExecer{}, "ref-1", pkg, software.InstallOptions{
		Architecture: "riscv64",
	})
	if err == nil || !strings.Contains(err.Error(), "architecture") {
		t.Fatalf("error=%v", err)
	}
}
