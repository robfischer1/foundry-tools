package checks

import (
	"fmt"
	"strings"
	"testing"
)

// A SURVIVING MUTANT IS A FINDING, and the mapping onto the schema's lattice is
// this file's own epistemics rather than a convenience.
//
// The mutation report is the atom whose output costs a reader the most: ~40 lines
// of table, prose and fenced lists for what is usually two or three facts. At
// depth 4 it is one line per cause.

// everyStatus is one gremlins report carrying each status the scorer knows,
// including two mutants on THE SAME LINE at different columns — the shape that
// decides whether the column belongs in the subject.
const everyStatus = `{"elapsed_time":1,"files":[{"file_name":"pkg/a.go","mutations":[
	{"type":"CONDITIONALS_BOUNDARY","status":"KILLED","line":1,"column":1},
	{"type":"CONDITIONALS_NEGATION","status":"LIVED","line":10,"column":5},
	{"type":"ARITHMETIC_BASE","status":"NOT COVERED","line":20,"column":7},
	{"type":"INCREMENT_DECREMENT","status":"TIMED OUT","line":30,"column":9},
	{"type":"CONDITIONALS_BOUNDARY","status":"NOT VIABLE","line":40,"column":11},
	{"type":"CONDITIONALS_NEGATION","status":"SKIPPED","line":41,"column":2},
	{"type":"CONDITIONALS_BOUNDARY","status":"LIVED","line":50,"column":11},
	{"type":"CONDITIONALS_NEGATION","status":"LIVED","line":50,"column":23}]}]}`

func findingFor(t *testing.T, fs []Finding, subject, cause string) Finding {
	t.Helper()
	for _, f := range fs {
		if f.Subject == subject && f.Cause == cause {
			return f
		}
	}
	t.Fatalf("no finding for %s / %s in %+v", subject, cause, fs)
	return Finding{}
}

// THE LATTICE MAPPING, asserted status by status. Each row is defended in
// mutantVerdict's own comment, and every one of those defences quotes this
// package rather than my reading of it.
func TestScoreGoMutationFindingsMapTheLattice(t *testing.T) {
	s, err := ScoreGoMutation([]byte(everyStatus), "", "diff", 1, GoMutationNoise{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// A KILLED MUTANT IS NOT A FINDING — it is the tests working. Neither is an
	// inert one: NOT VIABLE and SKIPPED were decided without running anything.
	// Five of the eight are worth reporting; the other three are the kill and the
	// two inert statuses.
	if len(s.Findings) != 5 {
		t.Fatalf("want 5 findings from 8 mutants (1 killed, 1 not-viable, 1 skipped carry none): got %d, %+v", len(s.Findings), s.Findings)
	}
	for _, f := range s.Findings {
		if strings.Contains(f.Detail, "KILLED") || strings.Contains(f.Detail, "NOT VIABLE") || strings.Contains(f.Detail, "SKIPPED") {
			t.Errorf("a kill or an inert mutant became a finding: %+v", f)
		}
		if f.Probe != "go:mutation" {
			t.Errorf("probe = %q, want the atom's id: %+v", f.Probe, f)
		}
		// THE SUBJECT IS file:line, NOT file:line:col — the column rides in the
		// detail so one line's mutants share a subject a reader can act on.
		if strings.Count(f.Subject, ":") != 1 {
			t.Errorf("subject %q carries a column; that belongs in the detail", f.Subject)
		}
		if !strings.Contains(f.Detail, "col ") {
			t.Errorf("the detail must keep the column — it is what identifies the mutant: %+v", f)
		}
	}

	// LIVED is `drifted`, NOT `violated`: this package is emphatic that a
	// survivor is "a HYPOTHESIS, not a finding ... a real test gap or equivalent
	// to the original", and asserting a defect it refuses to assert would put a
	// claim in the record the runner does not stand behind.
	lived := findingFor(t, s.Findings, "pkg/a.go:10", "CONDITIONALS_NEGATION")
	if lived.Verdict != VerdictDrifted {
		t.Errorf("LIVED = %q, want drifted — a survivor is a hypothesis: %+v", lived.Verdict, lived)
	}
	if !strings.Contains(lived.Detail, "col 5") {
		t.Errorf("detail = %q", lived.Detail)
	}

	// NOT COVERED is `violated` — the report calls it "the sharper of the two:
	// no test executes that code at all", which is a defect and not a maybe.
	notCovered := findingFor(t, s.Findings, "pkg/a.go:20", "ARITHMETIC_BASE")
	if notCovered.Verdict != VerdictViolated {
		t.Errorf("NOT COVERED = %q, want violated: %+v", notCovered.Verdict, notCovered)
	}

	// TIMED OUT is the fleet's one timeout decision: `excluded` under
	// mutant-timeout while TimedOutMutantIsDetected, the operator in the detail.
	timedOut := findingFor(t, s.Findings, "pkg/a.go:30", MutantTimeoutCause)
	if timedOut.Verdict != timedOutMutantVerdict(TimedOutMutantIsDetected) || timedOut.Verdict != VerdictExcluded {
		t.Errorf("TIMED OUT = %q, want excluded: %+v", timedOut.Verdict, timedOut)
	}
	if !strings.Contains(timedOut.Detail, "TIMED OUT INCREMENT_DECREMENT — "+MutantTimeoutAdvice) {
		t.Errorf("detail = %q", timedOut.Detail)
	}

	// TWO MUTANTS ON ONE LINE keep one subject and are told apart by cause —
	// which is exactly what a grouping key is for.
	a := findingFor(t, s.Findings, "pkg/a.go:50", "CONDITIONALS_BOUNDARY")
	b := findingFor(t, s.Findings, "pkg/a.go:50", "CONDITIONALS_NEGATION")
	if !strings.Contains(a.Detail, "col 11") || !strings.Contains(b.Detail, "col 23") {
		t.Errorf("the columns did not survive into the details: %q / %q", a.Detail, b.Detail)
	}
}

// COVERED-UNRUN IS `unanalyzable` BECAUSE THIS FILE SAYS SO: "not a miss, not a
// kill, unmeasured by name". It only exists when the coverage profile contradicts
// gremlins, so it needs a profile to reproduce.
func TestScoreGoMutationFindingsCallCoveredUnrunUnanalyzable(t *testing.T) {
	// A block that RUNS, starting on the mutant's own line to its right — the
	// switch-case misread this scorer corrects.
	const profile = "mode: set\nmod/pkg/a.go:20.12,22.3 1 1\n"
	const report = `{"go_module":"mod","elapsed_time":1,"files":[{"file_name":"pkg/a.go","mutations":[
		{"type":"CONDITIONALS_BOUNDARY","status":"NOT COVERED","line":20,"column":7}]}]}`
	s, err := ScoreGoMutation([]byte(report), profile, "diff", 1, GoMutationNoise{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.CoveredUnrun != 1 {
		t.Fatalf("the fixture did not produce a COVERED-UNRUN mutant: %+v", s)
	}
	if len(s.Findings) != 1 {
		t.Fatalf("one finding: %+v", s.Findings)
	}
	f := s.Findings[0]
	if f.Verdict != VerdictUnanalyzable {
		t.Errorf("COVERED-UNRUN = %q, want unanalyzable — unmeasured by name: %+v", f.Verdict, f)
	}
	// And NOT violated, which is what the uncorrected NOT COVERED would have been:
	// the correction has to reach the finding too, or depth 4 reports a defect
	// the scorer already decided was a misread.
	if f.Verdict == VerdictViolated {
		t.Error("the switch-case correction did not reach the finding")
	}
}

// A FORGIVEN MUTANT IS `excluded`, AND THE CAUSE IS THE FORGIVENESS.
//
// The schema requires a cause for an excluded finding and means it to answer "set
// aside by what". A reader deciding whether a pass was earned wants
// `3 × declaration`, not the operator — so the operator moves to the detail.
func TestScoreGoMutationFindingsExcludeWhatWasForgivenAndNameTheClass(t *testing.T) {
	const report = `{"elapsed_time":1,"files":[{"file_name":"a.go","mutations":[
		{"type":"ARITHMETIC_BASE","status":"NOT COVERED","line":3,"column":16}]}]}`
	noise, err := ParseGoMutationNoise([]byte(`{"noise":[{"file":"a.go","line":3,"column":16,"noise_reason":"declaration"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	s, err := ScoreGoMutation([]byte(report), "", "diff", 1, noise, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Forgiven) != 1 || len(s.Findings) != 1 {
		t.Fatalf("forgiven %d findings %d, want 1 1: %+v", len(s.Forgiven), len(s.Findings), s.Findings)
	}
	f := s.Findings[0]
	if f.Verdict != VerdictExcluded {
		t.Errorf("verdict = %q, want excluded: %+v", f.Verdict, f)
	}
	if f.Cause != "declaration" {
		t.Errorf("cause = %q, want the forgiveness class — that is what a reader groups on: %+v", f.Cause, f)
	}
	if !strings.Contains(f.Detail, "ARITHMETIC_BASE") {
		t.Errorf("the operator must survive into the detail: %+v", f)
	}
	// AN EXCLUSION NOBODY CAN READ IS A SUPPRESSION, which is why it is a finding
	// at all rather than simply absent.
	if !strings.Contains(f.Detail, "forgiven") {
		t.Errorf("the detail must say it was set aside: %+v", f)
	}
}

// THIS IS THE TEST THE DESIGN EXISTS FOR.
//
// An UNGRADED LIVED mutant and a MISSED LIVED mutant render as the SAME line in
// the summary — same status, same operator, same column padding — and only the
// section they are printed under tells them apart. Anything parsing the rendered
// report has to guess, and the guess that reads `drifted` where the truth is
// `unanalyzable` is the QUIET direction: it reports a hypothesis about the tests
// where the fact is that the gate cannot trust this verdict at all.
//
// Built from the score, there is no guess: the loop that decides the disposition
// is the loop that builds the finding.
func TestScoreGoMutationFindingsKeepADistinctionTheReportCannotExpress(t *testing.T) {
	const report = `{"elapsed_time":1,"files":[
		{"file_name":"cmd/tool/main.go","mutations":[{"type":"CONDITIONALS_NEGATION","status":"LIVED","line":7,"column":4}]},
		{"file_name":"pkg/a.go","mutations":[{"type":"CONDITIONALS_NEGATION","status":"LIVED","line":7,"column":4}]}]}`
	s, err := ScoreGoMutation([]byte(report), "", "diff", 1, GoMutationNoise{},
		GoMisgradedFiles{"cmd/tool/main.go": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Ungraded) != 1 || len(s.Missed) != 1 {
		t.Fatalf("the fixture must produce one of each: ungraded %d missed %d", len(s.Ungraded), len(s.Missed))
	}

	// THE RENDERING LOSES IT: strip the file name and the two lines are byte
	// identical, so the summary cannot tell a reader which is which.
	ungradedLine := strings.TrimPrefix(s.Ungraded[0], "cmd/tool/main.go")
	missedLine := strings.TrimPrefix(s.Missed[0], "pkg/a.go")
	if ungradedLine != missedLine {
		t.Fatalf("the premise of this test no longer holds — the rendered lines differ:\n ungraded %q\n missed   %q\nIf `where` now distinguishes them, say so and simplify; if it does not, this is still the reason findings are built from the score.", ungradedLine, missedLine)
	}

	// THE FINDINGS KEEP IT.
	ungraded := findingFor(t, s.Findings, "cmd/tool/main.go:7", "CONDITIONALS_NEGATION")
	missed := findingFor(t, s.Findings, "pkg/a.go:7", "CONDITIONALS_NEGATION")
	if ungraded.Verdict != VerdictUnanalyzable {
		t.Errorf("an ungraded mutant = %q, want unanalyzable — the verdict is about another package: %+v", ungraded.Verdict, ungraded)
	}
	if missed.Verdict != VerdictDrifted {
		t.Errorf("a missed survivor = %q, want drifted: %+v", missed.Verdict, missed)
	}
	if !strings.Contains(ungraded.Detail, "gremlins#268") {
		t.Errorf("the ungraded detail must name why it cannot be trusted: %+v", ungraded)
	}
}

// AN UNGRADED KILL IS STILL A FINDING — the one place a KILLED mutant becomes
// one. The verdict is about a different package, so "it passed" is not a fact
// this run established, and a reader needs to know the kill is not evidence.
func TestScoreGoMutationFindingsReportAnUngradedKill(t *testing.T) {
	const report = `{"elapsed_time":1,"files":[{"file_name":"cmd/tool/main.go","mutations":[
		{"type":"CONDITIONALS_BOUNDARY","status":"KILLED","line":3,"column":2}]}]}`
	s, err := ScoreGoMutation([]byte(report), "", "diff", 1, GoMutationNoise{},
		GoMisgradedFiles{"cmd/tool/main.go": true})
	if err != nil {
		t.Fatal(err)
	}
	if s.Killed != 0 || len(s.Ungraded) != 1 {
		t.Fatalf("killed %d ungraded %d, want 0 1: %+v", s.Killed, len(s.Ungraded), s)
	}
	if len(s.Findings) != 1 || s.Findings[0].Verdict != VerdictUnanalyzable {
		t.Fatalf("an ungraded kill is one unanalyzable finding: %+v", s.Findings)
	}
	if !strings.Contains(s.Findings[0].Detail, "KILLED") {
		t.Errorf("the detail must say what the untrusted verdict was: %+v", s.Findings[0])
	}
}

// ---- the seam into the verdict -------------------------------------------

// THE FINDINGS RIDE THE FINDINGS VERDICT AND NOTHING ELSE, which is the same
// rule findingsFor holds for the text-parsed atoms: a clean run found nothing to
// report, and a could-not-measure run did not measure.
//
// A DELIBERATE OMISSION, recorded so the next reader knows it was a decision: a
// state-0 run CAN hold unanalyzable mutants (COVERED-UNRUN with no survivors) and
// those findings are dropped here. They would be invisible anyway — ci_logs
// elides holding atoms from depth 3 on — and both state-0 reasons already say in
// prose what the run did not verify. If the renderer ever lists holding atoms,
// this is the line to revisit.
func TestGoMutationVerdictCarriesFindingsOnlyWhenItFoundSomething(t *testing.T) {
	const survivor = `{"elapsed_time":1,"files":[{"file_name":"a.go","mutations":[
		{"type":"CONDITIONALS_NEGATION","status":"LIVED","line":2,"column":3}]}]}`
	state, _, found := GoMutationVerdict(GoMutationRun{
		Report: []byte(survivor), Canary: CanaryOK, Classified: []byte(`{"noise":[]}`)})
	if state != 1 {
		t.Fatalf("the fixture must settle findings: state %d", state)
	}
	if len(found) != 1 || found[0].Verdict != VerdictDrifted || found[0].Subject != "a.go:2" {
		t.Fatalf("the survivors verdict carries its findings: %+v", found)
	}

	// A CLEAN RUN carries none.
	const allKilled = `{"elapsed_time":1,"files":[{"file_name":"a.go","mutations":[
		{"type":"T","status":"KILLED","line":1,"column":1}]}]}`
	if state, _, clean := GoMutationVerdict(GoMutationRun{
		Report: []byte(allKilled), Canary: CanaryOK, Classified: []byte(`{"noise":[]}`)}); state != 0 || len(clean) != 0 {
		t.Errorf("a clean run found nothing to report: state %d, %+v", state, clean)
	}

	// A BROKEN RUN carries none — it never measured, so it found nothing, and a
	// findings list here would read as defects the run proved.
	if state, _, broken := GoMutationVerdict(GoMutationRun{
		Report: []byte(survivor), Status: 3, Canary: CanaryOK, Classified: []byte(`{"noise":[]}`)}); state != 2 || len(broken) != 0 {
		t.Errorf("a broken run measured nothing: state %d, %+v", state, broken)
	}

	// AND A RUN WITH NO REPORT AT ALL carries none.
	if _, _, none := GoMutationVerdict(GoMutationRun{Canary: CanaryOK}); len(none) != 0 {
		t.Errorf("no report, no findings: %+v", none)
	}
}

// THE CAP REACHES THESE FINDINGS TOO. They do not come through FindingsOf, so
// they do not inherit capFindings for free — and a wide pull generates hundreds
// of survivors, which is the runaway the cap exists to stop: the record travels
// on ONE line of stdout. The full list stays in the summary either way.
func TestGoMutationVerdictCapsItsFindings(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"elapsed_time":1,"files":[{"file_name":"a.go","mutations":[`)
	for i := 0; i < findingCap+25; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		// One per line so each is its own subject.
		fmt.Fprintf(&b, `{"type":"CONDITIONALS_NEGATION","status":"LIVED","line":%d,"column":3}`, i+1)
	}
	b.WriteString(`]}]}`)

	state, _, found := GoMutationVerdict(GoMutationRun{
		Report: []byte(b.String()), Canary: CanaryOK, Classified: []byte(`{"noise":[]}`)})
	if state != 1 {
		t.Fatalf("the fixture must settle findings: state %d", state)
	}
	if len(found) != findingCap+1 {
		t.Fatalf("the cap plus its own finding: got %d, want %d", len(found), findingCap+1)
	}
	last := found[len(found)-1]
	if last.Verdict != VerdictExcluded || last.Cause != "finding-cap" {
		t.Fatalf("the cut is stated as an exclusion with a named cause: %+v", last)
	}
	if last.Probe != "go:mutation" {
		t.Errorf("the cut names the atom that cut: %+v", last)
	}
}

// THE MUTATION ATOM IS NOT IN FindingsOf, AND THAT IS THE DESIGN. Its findings come
// from the score; a text parser here would produce a second, lossier set from a
// rendering of data we already had.
func TestFindingsOfDoesNotParseTheMutationReport(t *testing.T) {
	report := "go:mutation: FINDINGS (exit 1)\n```\na.go:2:3  LIVED         CONDITIONALS_NEGATION\n```\n"
	if got := FindingsOf("go:mutation", report); got != nil {
		t.Errorf("the mutation report must not be text-parsed — the score is the source: %+v", got)
	}
}
