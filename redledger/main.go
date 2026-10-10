// redledger is conformance:red-ledger as a standalone check: it runs as its
// own CronJob under the fixed identity spiffe://notusmi.com/ci/prime/red-ledger
// (not in the gate lane, whose per-run job/gate/<name> identity hades
// refuses), granted plan_task_list and plan_task_read and nothing else. The
// logic is internal/redledger; this is only its process.
//
//	redledger [tree]      exit 0 pass or ABSENT, 1 findings, 2 could not run
package main

import (
	"context"
	"os"

	"dagger/foundry-tools/internal/hadescall"
	"dagger/foundry-tools/internal/redledger"
)

// exit is os.Exit, and a variable so main_test.go can run main: go:mutation
// grades a line no test executes as NOT COVERED.
var exit = os.Exit

func main() {
	exit(redledger.Main(context.Background(), os.Args[1:], hadescall.Run, os.Getenv, os.Stdout, os.Stderr))
}
