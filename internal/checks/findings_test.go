package checks

import (
	"strings"
	"testing"
)

// THE FIXTURES ARE REAL OUTPUT, not invented shapes. Every sample below was
// produced by an actual ourea gate on 2026-09-26 and copied verbatim; a parser
// tested against a format its author imagined is a parser tested against nothing.

// staticcheckOutput is `go:staticcheck` failing on ourea 286dc0e's predecessor —
// the orphaned method that reddened the gate earlier tonight.
const staticcheckOutput = `internal/verbs/cilogs_runs.go:132:15: func Deps.runSetSummary is unused (U1000)
`

// goTestOutput is `go:test-race` failing on ourea adf0f15, with the whole
// surrounding scroll of passing packages that a parser must ignore.
const goTestOutput = `ok  	git.notusmi.com/rob/ourea/internal/forgejo	(cached)
2026/09/26 01:40:34 INFO gatejob: queued — every engine is at its budget repo=a
--- FAIL: TestTheDequeuedLineCountsTheRunItStarted (0.01s)
    engines_unit_test.go:411: the dequeued line counts the run it started: ""
FAIL
FAIL	git.notusmi.com/rob/ourea/internal/gatejob	101.475s
ok  	git.notusmi.com/rob/ourea/internal/gc	(cached)
`

func TestFindingsOfAGoTestFailure(t *testing.T) {
	got := FindingsOf("go:test-race", goTestOutput)
	if len(got) != 1 {
		t.Fatalf("one failing test in that scroll, got %d: %+v", len(got), got)
	}
	f := got[0]
	if f.Subject != "TestTheDequeuedLineCountsTheRunItStarted" {
		t.Errorf("the subject is the test's NAME — the thing a reader re-runs: %q", f.Subject)
	}
	if f.Verdict != "violated" {
		t.Errorf("a failing test is a defect, not evidence of nothing: %q", f.Verdict)
	}
	if f.Cause != "test-failed" {
		t.Errorf("cause = %q; go test does not say WHY in a groupable form, so it is not invented", f.Cause)
	}
	if !strings.Contains(f.Detail, "the dequeued line counts the run it started") {
		t.Errorf("the detail is the test's own first line: %q", f.Detail)
	}
	if f.Probe != "go:test-race" {
		t.Errorf("probe names the atom: %q", f.Probe)
	}
}

// THE PASSING SCROLL IS NOT A FINDING. `ok` lines, log lines and the bare `FAIL`
// summary all appear in the same output, and a parser that matched loosely would
// report the package as a failing test.
func TestFindingsOfGoTestIgnoresEverythingElse(t *testing.T) {
	for _, noise := range []string{
		"ok  	git.notusmi.com/rob/ourea/internal/gc	(cached)",
		"FAIL",
		"FAIL	git.notusmi.com/rob/ourea/internal/gatejob	101.475s",
		"2026/09/26 01:40:34 INFO gatejob: queued",
		"--- PASS: TestSomething (0.01s)",
		"--- SKIP: TestOther (0.00s)",
	} {
		if got := FindingsOf("go:test", noise); len(got) != 0 {
			t.Errorf("%q must produce no finding, got %+v", noise, got)
		}
	}
}

// A SUBTEST'S FAILURE IS ITS OWN FINDING, indented under its parent — and both
// are reported, because a reader acts on the leaf.
func TestFindingsOfGoTestReadsSubtests(t *testing.T) {
	got := FindingsOf("go:test", `--- FAIL: TestParent (0.02s)
    --- FAIL: TestParent/the_case (0.01s)
        parent_test.go:12: want 2 got 3
`)
	if len(got) != 2 {
		t.Fatalf("the parent and the subtest are both findings: %+v", got)
	}
	if got[0].Subject != "TestParent" || got[1].Subject != "TestParent/the_case" {
		t.Fatalf("subjects: %q, %q", got[0].Subject, got[1].Subject)
	}
	// THE PARENT'S DETAIL IS NOT THE SUBTEST'S HEADER. A parent whose only
	// output is its child's FAIL line has no detail of its own, and borrowing
	// the child's would attribute the child's failure to the parent.
	if got[0].Detail != "" {
		t.Errorf("the parent borrowed its child's header as a detail: %q", got[0].Detail)
	}
	if !strings.Contains(got[1].Detail, "want 2 got 3") {
		t.Errorf("the subtest keeps its own detail: %q", got[1].Detail)
	}
}

// THE CAUSE IS THE CHECK'S OWN CODE where the tool gives one. `U1000` is exactly
// "a slug a machine can group on": thirty unused functions read as `30 × U1000`
// rather than thirty sentences, which is the whole saving depth 4 buys.
func TestFindingsOfStaticcheckUsesTheCheckCodeAsTheCause(t *testing.T) {
	got := FindingsOf("go:staticcheck", staticcheckOutput)
	if len(got) != 1 {
		t.Fatalf("one finding: %+v", got)
	}
	f := got[0]
	if f.Cause != "U1000" {
		t.Errorf("cause = %q, want the check's own code", f.Cause)
	}
	// THE SUBJECT IS file:line, NOT file:line:col — a column is precision a
	// reader does not navigate by, and it would split one line's findings into
	// several subjects.
	if f.Subject != "internal/verbs/cilogs_runs.go:132" {
		t.Errorf("subject = %q", f.Subject)
	}
	if f.Detail != "func Deps.runSetSummary is unused (U1000)" {
		t.Errorf("detail is the tool's own message: %q", f.Detail)
	}
}

// WITHOUT A CODE THE CAUSE IS THE ATOM, which still groups the findings by what
// found them rather than leaving them uncategorised. go vet writes no codes.
func TestFindingsOfGoVetFallsBackToTheAtomAsTheCause(t *testing.T) {
	got := FindingsOf("go:vet", "internal/tap/ci.go:412:3: unreachable code\n")
	if len(got) != 1 {
		t.Fatalf("one finding: %+v", got)
	}
	if got[0].Cause != "go:vet" {
		t.Errorf("cause = %q, want the atom's id as the fallback", got[0].Cause)
	}
	if got[0].Subject != "internal/tap/ci.go:412" {
		t.Errorf("subject = %q", got[0].Subject)
	}
}

// A POSITIONAL LINE WITH NO COLUMN still reads — gofmt-family tools omit it.
func TestFindingsOfPositionalWithoutAColumn(t *testing.T) {
	got := FindingsOf("go:vet", "main.go:7: something\n")
	if len(got) != 1 || got[0].Subject != "main.go:7" {
		t.Fatalf("a column-less position still reads: %+v", got)
	}
}

// AN UNRECOGNISED ATOM EMITS NOTHING, and that is the design rather than a gap:
// sixty-seven atoms run sixty-seven formats, and a fabricated subject in a record
// the door stores as fact is worse than an honest absence the reader already
// handles.
func TestFindingsOfAnUnknownAtomEmitsNothing(t *testing.T) {
	for _, atom := range []string{"python:ruff-check", "rust:cargo-clippy", "fleet:hadolint", "ops:yaml", ""} {
		if got := FindingsOf(atom, "some/file.go:1:1: something that looks parseable\n"); got != nil {
			t.Errorf("%q must emit nothing until someone writes its parser, got %+v", atom, got)
		}
	}
}

// EMPTY OUTPUT IS NOT A FINDING.
func TestFindingsOfNothing(t *testing.T) {
	for _, out := range []string{"", "   ", "\n\n\t\n"} {
		if got := FindingsOf("go:test", out); got != nil {
			t.Errorf("%q produced %+v", out, got)
		}
	}
}

// THE CAP BITES AND SAYS SO. A runaway suite must not turn the record — which
// travels on ONE line of stdout — into the payload this feature exists to shrink,
// and an exclusion nobody can read is a suppression, so the cut is a finding.
func TestFindingsOfCapsAndStatesTheCut(t *testing.T) {
	var b strings.Builder
	for i := 0; i < findingCap+50; i++ {
		b.WriteString("--- FAIL: TestNumber")
		b.WriteString(strings.Repeat("x", 1)) // keep the names distinct enough
		b.WriteString("\n")
	}
	got := FindingsOf("go:test", b.String())
	if len(got) != findingCap+1 {
		t.Fatalf("the cap plus its own finding: got %d, want %d", len(got), findingCap+1)
	}
	last := got[len(got)-1]
	if last.Verdict != "excluded" || last.Cause != "finding-cap" {
		t.Fatalf("the cut is stated as an exclusion with a named cause: %+v", last)
	}
	if !strings.Contains(last.Detail, "in its lines") {
		t.Errorf("and it says where the rest are: %q", last.Detail)
	}
}

// ---- the seam into the verdict -------------------------------------------

// FINDINGS RIDE ONLY A FINDINGS STATE. A pass found nothing; a cannot-run never
// looked, which is the distinction the three states exist to keep — so its error
// output must not become a list of defects the run "proved".
func TestVerdictOfCarriesFindingsOnlyForAFindingsState(t *testing.T) {
	def := AtomDef{ID: "go:staticcheck", Stage: StagePrepush, Lane: LaneGo}

	findings := VerdictOf(def, 1, staticcheckOutput)
	if findings.State != int(StateFindings) {
		t.Fatalf("exit 1 is findings: %+v", findings)
	}
	if len(findings.Findings) != 1 || findings.Findings[0].Cause != "U1000" {
		t.Fatalf("a findings verdict carries them: %+v", findings.Findings)
	}

	// Exit 0 with the same text: nothing found, whatever the output looks like.
	pass := VerdictOf(def, 0, staticcheckOutput)
	if len(pass.Findings) != 0 {
		t.Fatalf("a passing atom found nothing: %+v", pass.Findings)
	}

	// Exit 127 — a missing binary — is CANNOT RUN, and its output must not be
	// mined for defects: it is evidence of nothing.
	cannot := VerdictOf(def, 127, staticcheckOutput)
	if cannot.State != int(StateCannotRun) {
		t.Fatalf("127 is cannot-run: %+v", cannot)
	}
	if len(cannot.Findings) != 0 {
		t.Fatalf("a run that never looked found nothing: %+v", cannot.Findings)
	}
}

// AND AN ANNOUNCED ABSENCE CARRIES NONE EITHER — it is state 0 with result
// `absent`, and an atom that stood down examined nothing.
func TestVerdictOfAnAbsentAtomCarriesNoFindings(t *testing.T) {
	def := AtomDef{ID: "go:staticcheck", Stage: StagePrepush, Lane: LaneGo}
	v := VerdictOf(def, 0, "go:staticcheck: ABSENT - no go.mod\n")
	if v.Result != "absent" {
		t.Fatalf("result = %q, want absent", v.Result)
	}
	if len(v.Findings) != 0 {
		t.Fatalf("an absence found nothing: %+v", v.Findings)
	}
}
