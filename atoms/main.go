// atoms runs the fleet's in-process atoms over a repository and prints their
// verdict vector as JSON. The logic, and why it looks the way it does, is in
// internal/atoms; this is only its process.
//
//	atoms -root /src -base <sha> [-origin <url>] [-timeout 1m] [-workers N]
package main

import (
	"context"
	"os"
	"time"

	"dagger/foundry-tools/internal/atoms"
)

// exit is os.Exit, and a variable so main_test.go can run main: go:mutation
// grades a line no test executes as NOT COVERED.
var exit = os.Exit

func main() {
	exit(atoms.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, time.Now))
}
