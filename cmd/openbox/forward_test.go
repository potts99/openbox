// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestBuildForwardSSHArgs(t *testing.T) {
	t.Parallel()
	args, err := buildForwardSSHArgs("box.example", 2222, 13000, 3000, "dev")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-N", "-L", "13000:127.0.0.1:3000", "-p", "2222", "dev@box.example"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("got %#v want %#v", args, want)
	}
}

func TestBuildForwardSSHArgsRejectsBadInput(t *testing.T) {
	t.Parallel()
	if _, err := buildForwardSSHArgs("", 2222, 3000, 3000, "dev"); err == nil {
		t.Fatal("empty host accepted")
	}
	if _, err := buildForwardSSHArgs("box", 2222, 3000, 3000, "dev@x"); err == nil {
		t.Fatal("instance with @ accepted")
	}
	if _, err := buildForwardSSHArgs("box", 0, 3000, 3000, "dev"); err == nil {
		t.Fatal("bad ssh port accepted")
	}
}

func TestRunForwardPrint(t *testing.T) {
	t.Parallel()
	var stdout, stderr strings.Builder
	code := runForward([]string{"--host", "box.example", "--print", "dev", "3000"}, &stdout, &stderr, nil, nil)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ssh -N -L 3000:127.0.0.1:3000 -p 2222 dev@box.example") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestRunForwardExecutesSSH(t *testing.T) {
	t.Parallel()
	var stdout, stderr strings.Builder
	var got *exec.Cmd
	code := runForward([]string{"--host", "box.example", "--local", "4000", "dev", "3000"}, &stdout, &stderr,
		func(string) (string, error) { return "/usr/bin/ssh", nil },
		func(cmd *exec.Cmd) error { got = cmd; return nil },
	)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if got == nil || got.Path != "/usr/bin/ssh" {
		t.Fatalf("cmd=%v", got)
	}
	joined := strings.Join(got.Args[1:], " ")
	if joined != "-N -L 4000:127.0.0.1:3000 -p 2222 dev@box.example" {
		t.Fatalf("args=%q", joined)
	}
}
