// execmem runs a command and prints its exec's cgroup memory readings to
// stderr while it runs. The logic, and why it looks the way it does, is in
// internal/execmem; this is only its process.
//
//	execmem -- <command> [args...]
package main

import (
	"os"

	"dagger/foundry-tools/internal/execmem"
)

func main() { os.Exit(execmem.Main(os.Args[1:], os.Stderr)) }
