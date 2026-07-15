// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	schemaPath := flag.String("schema", "", "OpenAPI schema path")
	hashOutputPath := flag.String("hash-out", "", "schema hash output path")
	typesOutputPath := flag.String("types-out", "", "transport types output path")
	flag.Parse()
	contents, err := os.ReadFile(*schemaPath)
	if err != nil {
		panic(err)
	}
	generated := fmt.Sprintf("// SPDX-License-Identifier: AGPL-3.0-only\n\n// Code generated from api/openapi.yaml; DO NOT EDIT.\n\npackage generated\n\nconst OpenAPISHA256 = %q\n", fmt.Sprintf("%x", sha256.Sum256(contents)))
	if err := os.WriteFile(*hashOutputPath, []byte(generated), 0o644); err != nil {
		panic(err)
	}
	command := exec.Command("go", "run", "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.5.0", "-generate", "types,skip-prune", "-package", "generated", *schemaPath)
	types, err := command.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			panic(string(exitError.Stderr))
		}
		panic(err)
	}
	types = append([]byte("// SPDX-License-Identifier: AGPL-3.0-only\n\n"), types...)
	if err := os.WriteFile(*typesOutputPath, types, 0o644); err != nil {
		panic(err)
	}
}
