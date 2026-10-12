// bundleresign writes a KMS's signature into an OPA bundle. The logic, and why
// it exists, is in internal/bundleresign; this is only its process.
//
//	bundleresign input <bundle.tar.gz> <keyid>
//	bundleresign apply <bundle.tar.gz> <keyid> <sigfile>
package main

import (
	"os"

	"dagger/foundry-tools/internal/bundleresign"
)

// exit is os.Exit, a variable so a test can run main.
var exit = os.Exit

func main() { exit(bundleresign.Main(os.Args[1:], os.Stdout, os.Stderr)) }
