package checks

import (
	"strings"
)

// The rust lane's judgements, as pure functions over strings and ints.
//
// They live here rather than in package main for the reason golane.go does:
// nothing in main is unit-testable — `dag` panics without an engine — so every
// decision an atom makes about a tool's OUTPUT has to be a function this
// package can put a table under. In the shell shape each of these was a
// `grep -m1`, a `grep -q`, a `sed -n` or a `case` inside the atom's script,
// where no test could reach it.

// CargoExit translates CARGO'S OWN EXIT VOCABULARY into the three states.
//
// CARGO SAYS 101, NOT 1, WHEN THE COMMAND FAILED. `cargo clippy -- -D warnings`
// with a denied lint, and `cargo test` with a failing test, both end with the
// build or the test binary failing, and cargo reports that as its generic
// "the cargo command failed" code: 101. StateFor reads anything that is not 0
// or 1 as CANNOT RUN, and correctly so for a tool that speaks the convention —
// so without this the two atoms that matter most in the lane would file every
// real finding as "could not run", and the door would re-ask a red that is the
// committer's to fix rather than the engine's.
//
// The shell body this ports wrote the same mapping as `|| exit 1`: every
// non-zero code from cargo became FINDINGS. This is that line, narrowed to the
// one code it was actually there for, so a 127 (no binary) and a 137 (OOM)
// still reach StateFor as could-not-runs.
func CargoExit(code int) int {
	if code == 101 {
		return 1
	}
	return code
}

// FirstCargoError is the line a human reads first when the test suite would not
// BUILD: rustc's diagnostics start at column 0 with "error", and the first of
// them is the cause; everything after is fallout.
//
// It was `grep -m1 -E '^error' | cut -c1-200` — first match, cut to 200 — and
// the cut is kept because a rustc error line carries the whole expression it
// choked on and a verdict reason is read in a terminal. An output with no such
// line answers the empty string; the caller still files the FINDINGS, because
// "the tests did not build" was already decided by the exit code.
func FirstCargoError(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(line, "error") {
			continue
		}
		if r := []rune(line); len(r) > 200 {
			return string(r[:200])
		}
		return line
	}
	return ""
}

// CargoListsTests answers whether `cargo test --workspace -- --list` named a
// single test.
//
// NO TESTS IS A FINDING. A workspace with no #[test] prints "running 0 tests"
// and exits 0, which reads as a pass. Rob, 2026-09-11: nothing is built without
// tests. libtest's own --list names every test as `<path>: test` — one line per
// test, and benchmarks as `: benchmark` — so a listing with no such line is a
// workspace with nothing to run, red before the suite builds.
func CargoListsTests(list string) bool {
	for _, line := range strings.Split(list, "\n") {
		if strings.HasSuffix(strings.TrimSuffix(line, "\r"), ": test") {
			return true
		}
	}
	return false
}
