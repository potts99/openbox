// SPDX-License-Identifier: AGPL-3.0-only

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package sshgateway

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func validateDirectoryOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot determine directory owner")
	}
	if int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("owned by uid %d, expected uid %d", stat.Uid, os.Geteuid())
	}
	return nil
}
