// verdict prints a lane's reason and exits with its verdict, so the dagger CLI
// hands the door the code the lane settles on: 0 clean, 1 findings, 2 could
// not run.
//
//	verdict <code> <reason...>
//
// THE CLI EXITS WITH THE CODE OF THE EXEC ERROR A FUNCTION RETURNS. Measured
// 2026-09-14 against the cluster engine: an exec exiting 2, returned bare or
// wrapped in a Go error, made `dagger call` exit 2; a plain Go error made it
// exit 1. So a lane function never returns a plain error for a verdict — it
// ends on this exec, and a could-not-run cannot be misread as a finding.
//
// STANDARD LIBRARY ONLY, BUILT OUTSIDE ANY MODULE. It is the last thing a lane
// runs, including when nothing else could be fetched: a build that reached no
// Go proxy must still exit 2.
package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }

// run prints the reason and answers the code. A code outside the three the
// door reads is itself a could-not-run: nothing else may reach the settle.
func run(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "verdict: no code")
		return 2
	}
	code, err := strconv.Atoi(args[0])
	if err != nil || code < 0 || code > 2 {
		fmt.Fprintf(stderr, "verdict: %q is not a verdict (0, 1 or 2)\n", args[0])
		return 2
	}
	if reason := strings.Join(args[1:], " "); reason != "" {
		fmt.Fprintln(stderr, reason)
	}
	return code
}
