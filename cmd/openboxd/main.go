// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"os"

	"github.com/openbox-dev/openbox/internal/version"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(version.Version)
		return
	}

	fmt.Fprintln(os.Stderr, "openboxd: no command specified")
	os.Exit(2)
}
