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

// exit is os.Exit, and a variable so main_test.go can run main: go:mutation
// grades a line no test executes as NOT COVERED, and a one-line main is
// still a line.
var exit = os.Exit

func main() { exit(execmem.Main(os.Args[1:], os.Stderr)) }
