package checks

import (
	"fmt"
	"strings"
	"testing"
)

// THE ANVIL RUN, anvil@0c5355f, mutation Job forge/mutation-anvil-0c5355f-gbvcj
// (2026-10-02), exit 3. The log's mutant lines, the summary, the missed and
// timeout names are the transcript's own bytes. The 51 caught and 5 unviable
// names are SYNTHESISED — the transcript prints counts for those, not names —
// in the shape cargo-mutants writes its lists.
const anvilLog = `Found 58 mutants to test
ok       Unmutated baseline in 12s build + 10s test
 INFO Auto-set test timeout to 60s
TIMEOUT  crates/anvil/src/inotify.rs:148:5: replace poll_ms -> i32 with -1 in 0s build + 60s test
MISSED   crates/anvil/src/inotify.rs:248:5: replace max_user_watches -> Option<u64> with None in 0s build + 10s test
58 mutants tested in 6m: 1 missed, 51 caught, 5 unviable, 1 timeouts
`

const (
	anvilMissed  = "crates/anvil/src/inotify.rs:248:5: replace max_user_watches -> Option<u64> with None\n"
	anvilTimeout = "crates/anvil/src/inotify.rs:148:5: replace poll_ms -> i32 with -1\n"
)

func mutantList(n int, what string) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "crates/anvil/src/inotify.rs:%d:5: %s %d\n", 300+i, what, i)
	}
	return b.String()
}

func anvilRun() RustMutationRun {
	return RustMutationRun{Status: 3, Log: anvilLog, Missed: anvilMissed, Timeout: anvilTimeout,
		Caught: mutantList(51, "replace caught"), Unviable: mutantList(5, "replace unviable")}
}

func findingsBy(found []Finding, cause string) []Finding {
	var out []Finding
	for _, f := range found {
		if f.Cause == cause {
			out = append(out, f)
		}
	}
	return out
}

// Exit 3 with one survivor and one hang is a SURVIVOR REPORT: the missed
// mutant is a finding, the timeout is listed and does not vote.
func TestRustMutationExit3IsAReportNotABrokenRun(t *testing.T) {
	state, reason, found := RustMutationVerdict(anvilRun())
	if state != 1 {
		t.Fatalf("state %d, want 1 (findings):\n%s", state, reason)
	}
	for _, w := range []string{
		"1 viable mutant(s) survived the suite — see the survivor list below; 1 more timed out, counted as caught",
		"| 51 | 1 | 5 | 1 | 98% of 53 viable |",
		"**Survivors**", anvilMissed + "```",
		"**Timed out**", "Counted as caught, so they do not red the lane; " + MutantTimeoutAdvice, anvilTimeout + "```",
	} {
		if !strings.Contains(reason, w) {
			t.Errorf("reason lacks %q:\n%s", w, reason)
		}
	}
	if strings.Contains(reason, "CANNOT RUN") {
		t.Errorf("a timeout is not a broken run:\n%s", reason)
	}
	if len(found) != 2 {
		t.Fatalf("one finding per survivor and per timeout, got %+v", found)
	}
	missed := findingsBy(found, "mutant-missed")
	if len(missed) != 1 || missed[0] != (Finding{Verdict: VerdictDrifted, Subject: "crates/anvil/src/inotify.rs:248", Cause: "mutant-missed",
		Detail: "replace max_user_watches -> Option<u64> with None — survived the suite — a test gap or an equivalent mutant; read it before writing a test for it",
		Probe:  "rust:mutation"}) {
		t.Errorf("the survivor finding: %+v", missed)
	}
	hung := findingsBy(found, MutantTimeoutCause)
	if len(hung) != 1 || hung[0] != (Finding{Verdict: VerdictExcluded, Subject: "crates/anvil/src/inotify.rs:148", Cause: MutantTimeoutCause,
		Detail: "replace poll_ms -> i32 with -1 — timed out after 60s of test, the timeout: " + MutantTimeoutAdvice,
		Probe:  "rust:mutation"}) {
		t.Errorf("the timeout finding: %+v", hung)
	}
}

// Timeouts alone, with plenty caught, are a pass — and the timeouts are still
// findings, so a reader sees what each one costs.
func TestRustMutationTimeoutsWithPlentyCaughtPass(t *testing.T) {
	run := anvilRun()
	run.Missed = ""
	state, reason, found := RustMutationVerdict(run)
	if state != 0 {
		t.Fatalf("state %d, want 0:\n%s", state, reason)
	}
	if !strings.Contains(reason, "every viable mutant was caught (51 by a failing test, 1 by a timeout, of 52)") || !strings.Contains(reason, anvilTimeout+"```") {
		t.Errorf("reason:\n%s", reason)
	}
	if len(found) != 1 || found[0].Cause != MutantTimeoutCause || found[0].Verdict != VerdictExcluded {
		t.Errorf("the timeout must be listed, excluded: %+v", found)
	}
}

// A timeout whose test time the log does not carry still says it hit the
// timeout.
func TestRustMutationTimeoutWithoutItsLogLine(t *testing.T) {
	run := anvilRun()
	run.Log = "58 mutants tested in 6m: 1 missed, 51 caught, 5 unviable, 1 timeouts"
	_, _, found := RustMutationVerdict(run)
	hung := findingsBy(found, MutantTimeoutCause)
	if len(hung) != 1 || !strings.Contains(hung[0].Detail, "timed out at the test timeout: ") {
		t.Errorf("%+v", hung)
	}
}

// THE GUARD, both sides of each arm.
func TestRustTimeoutGuard(t *testing.T) {
	for _, c := range []struct {
		caught, missed, timeout int
		want                    bool
	}{
		{51, 1, 1, false},
		{0, 0, 0, false},  // no timeouts, nothing to guard
		{5, 0, 0, false},  // no timeouts
		{0, 0, 1, true},   // caught nothing but by timeout
		{0, 3, 1, true},   // caught nothing, missed some
		{1, 0, 1, false},  // exactly half: not more than half
		{1, 0, 2, true},   // more than half
		{2, 1, 3, false},  // 3 of 6: half
		{2, 0, 3, true},   // 3 of 5
		{10, 0, 9, false}, // 9 of 19
		{9, 0, 10, true},  // 10 of 19
	} {
		if got := RustTimeoutGuard(c.caught, c.missed, c.timeout); got != c.want {
			t.Errorf("RustTimeoutGuard(%d caught, %d missed, %d timeout) = %v, want %v", c.caught, c.missed, c.timeout, got, c.want)
		}
	}
}

// Zero caught and some timeouts is could-not-run, with the counts.
func TestRustMutationAllTimeoutsCannotRun(t *testing.T) {
	run := RustMutationRun{Status: 3, Log: anvilLog, Timeout: anvilTimeout + "src/x.rs:1:1: replace y\n", Unviable: "u\n"}
	state, reason, found := RustMutationVerdict(run)
	if state != 2 {
		t.Fatalf("state %d, want 2:\n%s", state, reason)
	}
	if !strings.HasPrefix(reason, "CANNOT RUN - 2 of 2 viable mutant(s) timed out and 0 were caught by a failing test") || !strings.Contains(reason, "| 0 | 0 | 1 | 2 |") {
		t.Errorf("reason:\n%s", reason)
	}
	if found != nil {
		t.Errorf("a could-not-run carries no findings: %+v", found)
	}
	// The majority arm, with some caught.
	run.Caught = "c\n"
	if state, reason, _ = RustMutationVerdict(run); state != 2 || !strings.Contains(reason, "2 of 3 viable") {
		t.Errorf("2 of 3 timed out: state %d\n%s", state, reason)
	}
	run.Caught = "c\nd\n"
	if state, reason, _ = RustMutationVerdict(run); state != 0 {
		t.Errorf("2 of 4 timed out is not a majority: state %d\n%s", state, reason)
	}
}

// THE DECISION, flipped: a timeout is unmeasured, so any run with one is
// could-not-run — the survivors and the counts still in the reason — and the
// timeout's word is unanalyzable.
func TestRustMutationTimeoutFlippedToUnmeasured(t *testing.T) {
	state, reason, found := rustMutationVerdict(anvilRun(), false)
	if state != 2 {
		t.Fatalf("state %d, want 2:\n%s", state, reason)
	}
	for _, w := range []string{"CANNOT RUN - 1 mutant(s) timed out and a timeout is unmeasured", "51 caught, 1 missed", anvilMissed, "Not counted as caught", "| 51 | 1 | 5 | 1 | 96% of 53 viable |"} {
		if !strings.Contains(reason, w) {
			t.Errorf("reason lacks %q:\n%s", w, reason)
		}
	}
	if found != nil {
		t.Errorf("%+v", found)
	}
	// Flipped, a run with no timeout is untouched: survivors are findings.
	run := anvilRun()
	run.Status, run.Timeout = 2, ""
	if state, reason, found := rustMutationVerdict(run, false); state != 1 || len(found) != 1 || strings.Contains(reason, "timed out") {
		t.Errorf("no timeout, flipped: state %d, %+v\n%s", state, found, reason)
	}
	if timedOutMutantVerdict(false) != VerdictUnanalyzable || timedOutMutantVerdict(true) != VerdictExcluded {
		t.Error("the decision's two words")
	}
}

// A baseline that failed is could-not-run IN THE TOOL'S WORDS — never an empty
// string after the colon, which is what anvil's run read.
func TestRustMutationCannotRunSaysWhy(t *testing.T) {
	baseline := "Found 12 mutants to test\nFAILED   Unmutated baseline in 3s build + 1s test\nERROR cargo test failed in an unmutated tree, so no mutants were tested\n"
	state, reason, found := RustMutationVerdict(RustMutationRun{Status: 4, Log: baseline})
	if state != 2 || found != nil {
		t.Fatalf("state %d, found %+v", state, found)
	}
	for _, w := range []string{"cargo mutants exited 4", ": ERROR cargo test failed in an unmutated tree, so no mutants were tested\n", "FAILED   Unmutated baseline"} {
		if !strings.Contains(reason, w) {
			t.Errorf("reason lacks %q:\n%s", w, reason)
		}
	}
	// The summary line wins the headline over an error line.
	_, reason, _ = RustMutationVerdict(RustMutationRun{Status: 70, Log: "error: a\n7 mutants tested in 1m: 7 caught\n"})
	if !strings.Contains(reason, "a crash): 7 mutants tested in 1m: 7 caught\n") {
		t.Errorf("the summary line is the headline:\n%s", reason)
	}
	// An outcome exit whose list did not read is not a report.
	for _, run := range []RustMutationRun{{Status: 3, Log: anvilLog, Caught: "c\n"}, {Status: 2, Log: anvilLog, Caught: "c\n"}} {
		state, reason, _ := RustMutationVerdict(run)
		if state != 2 || !strings.Contains(reason, "mutants.out lists no mutant with that outcome") || !strings.Contains(reason, ": 58 mutants tested in 6m") {
			t.Errorf("exit %d with an empty list: state %d\n%s", run.Status, state, reason)
		}
	}
}

func TestBoundedTail(t *testing.T) {
	if got := boundedTail("a\nb\nc\n", 2, 100); got != "b\nc" {
		t.Errorf("lines: %q", got)
	}
	if got := boundedTail("abcdef", 5, 6); got != "abcdef" {
		t.Errorf("at the limit nothing is cut: %q", got)
	}
	if got := boundedTail("abcdef", 5, 3); got != "…def" {
		t.Errorf("bytes: %q", got)
	}
	// Bytes that never start a rune run the cut off the end, not past it.
	if got := boundedTail("\x80\x80\x80", 5, 2); got != "…" {
		t.Errorf("no rune start: %q", got)
	}
	// é is two bytes; a cut inside one moves forward to the next rune.
	if got := boundedTail("éé", 5, 3); got != "…é" {
		t.Errorf("runes: %q", got)
	}
}

func TestRustSummaryLine(t *testing.T) {
	if got := rustSummaryLine(anvilLog); got != "58 mutants tested in 6m: 1 missed, 51 caught, 5 unviable, 1 timeouts" {
		t.Errorf("%q", got)
	}
	if got := rustSummaryLine("1 mutant tested in 2s: 1 caught"); got != "1 mutant tested in 2s: 1 caught" {
		t.Errorf("singular: %q", got)
	}
	if got := rustSummaryLine("Found 58 mutants to test"); got != "" {
		t.Errorf("%q", got)
	}
}

func TestRustMutantFindingsKeepAnUnparsedLineWhole(t *testing.T) {
	// A blank line BEFORE a mutant is skipped, not the end of the list, and a
	// line's surrounding space is not part of its name.
	got := rustMutantFindings("\n  odd line \r\n\n", VerdictDrifted, "c", func(string) string { return "w" })
	if len(got) != 1 || got[0].Subject != "odd line" || got[0].Detail != "odd line — w" {
		t.Errorf("%+v", got)
	}
}

// A wide pull's findings are capped like every other atom's, and the cut says so.
func TestRustMutationFindingsAreCapped(t *testing.T) {
	run := RustMutationRun{Status: 2, Missed: mutantList(findingCap+1, "replace missed"), Caught: mutantList(findingCap, "replace caught")}
	_, _, found := RustMutationVerdict(run)
	if len(found) != findingCap+1 || found[findingCap].Cause != "finding-cap" {
		t.Errorf("%d findings, last %+v", len(found), found[len(found)-1])
	}
}
