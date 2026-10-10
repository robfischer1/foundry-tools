// redledger is conformance:red-ledger as a standalone check: it runs as its
// own Job under a fixed identity (not in the gate lane, whose per-run
// job/gate/<name> identity hades refuses). The logic is internal/redledger.
//
//	redledger [tree]      exit 0 pass or ABSENT, 1 findings, 2 could not run
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"dagger/foundry-tools/internal/hadescall"
	"dagger/foundry-tools/internal/redledger"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	tree := "."
	if len(args) > 1 {
		fmt.Fprintln(stderr, "usage: redledger [tree]")
		return 2
	}
	if len(args) == 1 {
		tree = args[0]
	}
	ask := redledger.HadesAsk(func(ctx context.Context, a []string, out, errs *strings.Builder) int {
		return hadescall.Run(ctx, a, os.Getenv, out, errs)
	})
	r := redledger.Check(context.Background(), os.DirFS(tree), ask)
	fmt.Fprint(stdout, redledger.Render(r))
	return r.State
}
