// pinsync moves every pin that has to follow a version Renovate bumped: the
// URL and member literals, checks.ToolSHA256, tools.toml and the python lock.
// The logic, and why each of those cannot be Renovate's, is internal/pinsync;
// this is only its process. renovate-config runs it as a postUpgradeTask.
//
//	pinsync [-C dir] [-n]   exit 0 in step, 1 -n found drift, 2 could not run
package main

import (
	"context"
	"os"

	"dagger/foundry-tools/internal/pinsync"
)

// exit is os.Exit, and a variable so main_test.go can run main: go:mutation
// grades a line no test executes as NOT COVERED.
var exit = os.Exit

func main() {
	exit(pinsync.Main(context.Background(), os.Args[1:], os.Getenv, pinsync.Client(), os.Stdout, os.Stderr))
}
