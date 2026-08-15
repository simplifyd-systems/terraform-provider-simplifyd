//go:build tools

// Package tools pins the code-generation binaries this repo runs but never
// imports. Without a real import somewhere in the module, `go mod tidy` prunes
// them and `make docs` fails to resolve the package.
package main

import (
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)
