package checks

import (
	"strconv"
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

// MutationScore reads the integer the mutation script's score phase wrote to
// MUT_DIR/verdict: 0 clean, 1 survivors, 2 could not measure.
//
// THE SCORE PHASE IS THE ONE THAT DECIDES, which is why the atom exits with
// what this returns rather than with any phase's own status. An empty file —
// or one holding something that is not a number — is not a verdict, and the
// atom says CANNOT RUN rather than guessing: the shell body's `exit "$v"`
// would have failed the shell itself on a non-integer, which is the same
// refusal wearing a worse error message.
func MutationScore(verdictFile string) (int, bool) {
	s := strings.TrimSpace(verdictFile)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// RustCriticalModules is criticalModules under an exported name package main
// can call.
//
// THE NAME IS GROUP-SCOPED ON PURPOSE, and it is temporary. Three lane ports
// land this same parse at once (python, rust, ts all read critical_modules);
// one exported `CriticalModules` declared in three files is one compile error
// at the merge. Tesla19 collapses the copies into a single exported function
// and deletes this wrapper.
func RustCriticalModules(answers string) string { return criticalModules(answers) }

// criticalModules reads the repository's `critical_modules:` declaration out of
// .copier-answers.yml — THE ONE REPO FACT THE MUTATION LANE READS.
//
// A repo has no say in anything that runs (Rob, 2026-09-11): the first cut of
// the mutation atoms sourced a repo-root ci/mutation.env of MUT_* knobs, and
// since the scripts honour MUT_GATE=false that file was a one-line switch to
// turn a fleet gate off. It is gone. critical_modules survives because it
// declares WHAT matters, not how hard to look — and it is read where the
// template question put it, the same string the retired mutation.yml rendered
// into its `modules` input.
//
// This is `sed -n 's/^critical_modules:[[:space:]]*//p' | head -1` plus the two
// quote-stripping seds, exactly: the FIRST line that starts with the key, the
// whitespace after the colon dropped, and ONE leading and ONE trailing quote
// character removed. A missing file or a missing key is the empty string, which
// the caller reads as "not declared" — an absence, never a finding.
func criticalModules(answers string) string {
	for _, line := range strings.Split(answers, "\n") {
		line = strings.TrimSuffix(line, "\r")
		rest, ok := strings.CutPrefix(line, "critical_modules:")
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		if len(rest) > 0 && (rest[0] == '\'' || rest[0] == '"') {
			rest = rest[1:]
		}
		if len(rest) > 0 && (rest[len(rest)-1] == '\'' || rest[len(rest)-1] == '"') {
			rest = rest[:len(rest)-1]
		}
		return rest
	}
	return ""
}

// ModulesDeclared reports whether a critical_modules value declares anything.
//
// The shell asked `[ -n "$(printf '%s' "$MODS" | tr -d ' ')" ]` — a value of
// nothing but spaces is not a declaration — and an undeclared list is the whole
// diff, not an opt-out.
func ModulesDeclared(mods string) bool {
	return strings.TrimSpace(mods) != ""
}
