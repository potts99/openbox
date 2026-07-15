// SPDX-License-Identifier: AGPL-3.0-only

package software_test

import (
	"context"
	"strings"
	"testing"

	runtimeapi "github.com/openbox-dev/openbox/internal/runtime"
	"github.com/openbox-dev/openbox/internal/software"
)

type recordingExecer struct {
	commands [][]string
	results  map[string]runtimeapi.ExecResult
	errFor   map[string]error
}

func (e *recordingExecer) Exec(_ context.Context, req runtimeapi.ExecRequest) (runtimeapi.ExecResult, error) {
	e.commands = append(e.commands, append([]string{}, req.Command...))
	key := strings.Join(req.Command, " ")
	if err, ok := e.errFor[key]; ok {
		return runtimeapi.ExecResult{}, err
	}
	if res, ok := e.results[key]; ok {
		return res, nil
	}
	return runtimeapi.ExecResult{ExitCode: 0}, nil
}

func TestInstallRunsPinsThenVerify(t *testing.T) {
	t.Parallel()
	pkg, ok := software.DefaultCatalog().Get("pi")
	if !ok {
		t.Fatal("missing pi")
	}
	execer := &recordingExecer{}
	if err := software.Install(context.Background(), execer, "ref-1", pkg); err != nil {
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
	err := software.Install(context.Background(), execer, "ref-1", pkg)
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
	err := software.Install(context.Background(), execer, "ref-1", pkg)
	if err == nil {
		t.Fatal("expected install failure")
	}
}
