// SPDX-License-Identifier: AGPL-3.0-only

//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package sshgateway

import "os"

func validateDirectoryOwner(os.FileInfo) error { return nil }
