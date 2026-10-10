// Package verdict is the exit a lane ends on: it prints the lane's reason and
// answers its verdict, so the dagger CLI hands the door the code the lane
// settles on — 0 clean, 1 findings, 2 could not run. The verdict binary
// (the module's verdict/) is its thin main.
//
// THE CLI EXITS WITH THE CODE OF THE EXEC ERROR A FUNCTION RETURNS. Measured
// 2026-09-14 against the cluster engine: an exec exiting 2, returned bare or
// wrapped in a Go error, made `dagger call` exit 2; a plain Go error made it
// exit 1. So a lane function never returns a plain error for a verdict — it
// ends on this exec, and a could-not-run cannot be misread as a finding.
//
// STANDARD LIBRARY ONLY, so the binary builds when nothing can be fetched.
// Measured the same day on Go 1.26.6 with GOPROXY=off and an empty module
// cache: a std-only package inside this module builds, and hadescall
// (go-spiffe) does not.
//
// THE LOGIC LIVES HERE, NOT IN main: gremlins tests a `package main` under a
// subdirectory against the module root, which never runs it (see
// internal/hadescall).
package verdict

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Run prints the reason and answers the code. A code outside the three the
// door reads is itself a could-not-run: nothing else may reach the settle.
func Run(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "verdict: no code")
		return 2
	}
	code, err := strconv.Atoi(args[0])
	if err != nil || code < 0 || code > 2 {
		fmt.Fprintf(stderr, "verdict: %q is not a verdict (0, 1 or 2)\n", args[0])
		return 2
	}
	reason := strings.Join(args[1:], " ")
	// `verdict 1 @/reason` reads the reason from a file: a long reason as an
	// argument is echoed by the engine into the exec's span title, so the same
	// text is printed twice. A file it cannot read is a could-not-run.
	if path, ok := strings.CutPrefix(reason, "@"); ok && len(args) == 2 {
		b, err := readFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "verdict: cannot read the reason: %v\n", err)
			return 2
		}
		reason = strings.TrimRight(string(b), "\n")
	}
	if reason != "" {
		fmt.Fprintln(stderr, reason)
	}
	return code
}

// readFile is how the @file form reads, and a variable so a test can say what
// the file holds.
var readFile = os.ReadFile
