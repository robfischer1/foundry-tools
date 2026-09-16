package checks

import (
	"strings"
)

// GoHasTestFiles reads the output of
//
//	go list -f '{{len .TestGoFiles}}{{len .XTestGoFiles}}' ./...
//
// — one line per package, two counts run together — and answers whether ANY
// package carries a test file. A module where every line is some spelling of
// zero has nothing for `go test` to run, and go would print "[no test files]"
// per package and exit 0; that is the green this check exists to refuse.
func GoHasTestFiles(goListOutput string) bool {
	for _, line := range strings.Split(goListOutput, "\n") {
		if strings.Trim(strings.TrimSpace(line), "0") != "" {
			return true
		}
	}
	return false
}

// argvBudget is the byte budget a file list is allowed to occupy as arguments
// before it goes in as a file instead.
//
// Linux's ARG_MAX is 2 MiB and the whole argv plus the environment has to fit
// inside it. 128 KiB is a sixteenth of that and is NOT a measured fleet
// maximum — no repository here has been counted against it, and the expected
// answer for every one of them is false. The budget exists so that a tree
// nobody anticipated meets a documented second path rather than an E2BIG from
// the kernel, which would reach the vector as an engine error naming nothing.
const argvBudget = 128 << 10

// NeedsArgFile reports whether a file list is too long to hand a tool as
// arguments, so the caller writes it NUL-joined to a file and runs the tool
// through xargs -0 -a instead. One NUL per file, so the joined length is
// exactly what the kernel would carry.
func NeedsArgFile(files []string) bool {
	n := 0
	for _, f := range files {
		n += len(f) + 1
		if n > argvBudget {
			return true
		}
	}
	return false
}

// GovulncheckExit translates govulncheck's exit vocabulary into the atom's:
// govulncheck answers 3 when it FOUND A VULNERABILITY, 0 when it found none,
// and 1 or 2 when it could not load or run. StateFor reads a 3 as could-not-
// run, so without this a real finding would be filed as a broken check — the
// old shell body's `|| exit 1` hid the code entirely, and the first typed cut
// passed it through raw (caught by the paper engine, 2026-09-12).
func GovulncheckExit(code int) int {
	if code == 3 {
		return 1
	}
	return code
}
