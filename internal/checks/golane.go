package checks

import (
	"errors"
	"fmt"
	"strconv"
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

// MutationVerdict reads what the mutation lane's score phase wrote — the
// verdict file and the reason file — and answers the atom's state and the line
// it reports.
//
// THE SCRIPT'S VERDICT IS THE ATOM'S. The canonical scripts at
// foundry-stocks ci/lib/mutation/<lang>.sh reach the three states themselves —
// 0 clean, 1 survivors, 2 could not measure — and the shell form of this was
// `exit "$v"` after a `cat`. It is a function here because "the file is
// missing", "the file is empty" and "the file says 0" are three different
// answers that `cat … 2>/dev/null` renders as one empty string, and because
// nothing in package main is unit-testable.
//
// AN EMPTY OR MISSING VERDICT IS NEVER A PASS. A score phase that wrote nothing
// measured nothing, and a mutation gate reporting success without having
// mutated anything is foundry-stocks#4415's zero-file scan wearing another hat.
// A verdict that is not an integer is the same refusal: the shell's `exit "$v"`
// would have answered 2 by way of an error message, and this says so instead.
//
// The state is returned RAW rather than clamped: StateFor already maps anything
// that is not 0 or 1 to cannot-run, so a script that one day writes 3 is a
// could-not-run and not a silent pass.
//
// It takes no atom id, because the four mutation atoms differ only in which
// script wrote the two files.
//
// ONE READER FOR FOUR LANES. The lane ports each landed their own — golane's
// MutationVerdict, pythonlane's MutationOutcome (an `ok` bool where this
// returns an error) and rustlane's MutationScore (the verdict file alone) —
// because three branches could not declare one exported name without colliding
// at the merge. This is the survivor, and it is the strictest of the three: a
// missing, empty or non-integer verdict is state 2 WITH a sentence saying which
// of those it was, where the other two answered a bare false the caller had to
// turn back into prose. The reason-file synthesis went with it, so a score
// phase that wrote a verdict and no reason now says so instead of rendering
// "<atom>: " and nothing after it.
func MutationVerdict(verdictText, reasonText string) (int, string, error) {
	v := strings.TrimSpace(verdictText)
	if v == "" {
		return 2, "", errors.New("the score phase wrote no verdict")
	}
	state, err := strconv.Atoi(v)
	if err != nil {
		return 2, "", fmt.Errorf("the score phase wrote %q, which is not a verdict", v)
	}
	reason := strings.TrimSpace(reasonText)
	if reason == "" {
		// The old body printed "<atom>: " and nothing after it. A verdict with
		// no sentence attached is still a verdict, but it should say that it
		// arrived bare rather than look like a truncated one.
		reason = "the score phase wrote verdict " + v + " and no reason"
	}
	return state, reason, nil
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
