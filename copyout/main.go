// copyout runs a build and copies its outputs out of a cache volume in the
// same exec. The logic, and why it exists, is in internal/copyout; this is
// only its process.
//
//	copyout SRC=DST... -- <command> [args...]
package main

import (
	"os"

	"dagger/foundry-tools/internal/copyout"
)

// exit is os.Exit, a variable so main_test.go can run main: go:mutation grades
// a line no test executes as NOT COVERED.
var exit = os.Exit

func main() { exit(copyout.Main(os.Args[1:], os.Stdout, os.Stderr)) }
