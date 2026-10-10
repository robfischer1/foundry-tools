package checks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// THE GO MUTATION GATE'S DECISIONS, in Go. They were foundry-stocks'
// ci/lib/mutation/go.sh score phase and go_score.py; the atom (atoms_go.go
// goMutation) now runs the measurement as plain execs and settles here.
//
// EVERY COUNT COMES FROM files[].mutations[].status, never gremlins' summary
// block: its mutants_total excludes not-covered AND timed-out mutants — it read
// 30 on a run that generated 698 — so the summary cannot answer the question
// this gate asks.
//
// NOT COVERED IS NOT ALWAYS TRUE. gremlins asks whether a mutant's line:column
// falls inside a covered block (pos.Column >= block start column), and Go's
// cover tool starts a `switch` case block AFTER the case expression, so every
// mutant in the expression reads uncovered on a line that runs every call
// (measured on stellar-core-go: 30 of 107, ops/ops.go at 100% statement
// coverage reported 13). The signature is exact — a covered block that STARTS
// on the mutant's own line at a column to its right — and such a mutant is
// COVERED-UNRUN: not a miss, not a kill, unmeasured by name.
//
// AND A RUN CAN REPORT KILLS IT NEVER EARNED, two different ways: a harness
// that never starts the test child scores every mutant KILLED, and a mutant in a
// `package main` is graded against the module root instead of against its own
// package. Both produce a report that reads like a measurement, so both are
// answered by a control rather than by anything in the report —
// gomutationcanary.go holds the two controls and says what each one proves. ms
// per mutant is reported for a reader and gates nothing.

// GoMutationScore is a gremlins report scored the way the gate reads it.
type GoMutationScore struct {
	Killed, Lived, NotCovered, TimedOut, CoveredUnrun, Inert, Generated int
	// ProfileDisagrees counts the NOT COVERED mutants for which the profile the
	// LANE gathered has a covered block starting on the mutant's own line. Each
	// one is a position two coverage reads disagree about, and the lane's read is
	// the one that ran `go test -cover` in this container moments earlier.
	//
	// ONE OF THEM IS NOISE; ALL OF THEM IS A BROKEN MAP. A single disagreement is
	// the switch-case misread CoveredUnrun already names. But a run that killed
	// nothing, lost nothing, and disagrees about EVERY uncovered mutant did not
	// measure the tests at all — see GoMutationVerdict.
	ProfileDisagrees int
	// Missed are the LIVED and NOT COVERED mutants, as file:line:col status type.
	Missed []string
	// Forgiven are the survivors the testkit's classifier named unkillable by
	// construction, each with its reason. They are not in Missed and not in
	// the kill rate's denominator: a mutant no test could ever kill measures
	// nothing about the tests.
	Forgiven []string
	// Ungraded are the mutants this runner graded against the wrong package —
	// every mutant in a `package main` below the module root while gremlins#268
	// stands (GoMisgradedFiles says why not every main file). They carry a
	// KILLED or LIVED verdict that is about a DIFFERENT package, so they are in
	// neither the kills nor the misses and not in the rate's denominator: a
	// verdict the gate cannot trust is not a measurement it can count.
	Ungraded []string
	// Findings are the mutants that are worth a reader's attention, one entry
	// each, in the findings schema's shape.
	//
	// BUILT HERE AND NOT PARSED BACK OUT OF Summary, because the rendering is
	// LOSSY in a way that matters: an Ungraded LIVED mutant and a Missed LIVED
	// mutant produce the identical `where` line, and only their section in the
	// report tells them apart. A text reader would have to guess, and the guess
	// that reads `drifted` where the truth is `unanalyzable` is the quiet
	// direction. Here the disposition is known exactly, because this is the loop
	// that decides it.
	//
	// A KILLED MUTANT IS NOT A FINDING. It is the tests working, and the counts
	// already carry it — a run of 25 kills and 2,763 inert would otherwise emit
	// thousands of entries and become the payload the cap exists to stop.
	Findings []Finding
	// Scored is every mutant as this scorer decided it, in report order, its
	// file as the report names it (module-relative). It is what a run's
	// gradings are built from (gradings.go): the same decisions as the counts
	// and findings above, one record each.
	Scored []ScoredMutant
	// TimedOutPct is timed-out mutants over every mutant generated.
	TimedOutPct float64
	// MsPerMutant is wall clock per mutant that ran, times the workers; -1 when
	// the report carries no elapsed time or nothing ran.
	MsPerMutant float64
	// Summary is the human- and agent-readable account.
	Summary string
}

// Viable are the mutants this run graded and can be scored on: killed, lived and
// never covered. Forgiven, ungraded, timed-out, covered-unrun and inert mutants
// are each excluded for their own reason, and every one of those reasons is a
// statement that the mutant measures nothing about the tests.
func (s GoMutationScore) Viable() int { return s.Killed + s.Lived + s.NotCovered }

// profileRow is one block of a coverage profile:
// file:startLine.startCol,endLine.endCol statements count. The "mode:" header
// and anything malformed do not match, and a row that does not match is not a
// verdict.
var profileRow = regexp.MustCompile(`^(.+):(\d+)\.(\d+),\d+\.\d+ \d+ (\d+)$`)

// coveredBlocks reads a `go test -coverprofile` into the start (line, column) of every
// block that ran, keyed by the path the PROFILE names — `<import-path>/<relpath>`.
//
// IT NO LONGER TAKES A MODULE NAME, and that is the fix rather than a tidy-up. It used to
// strip `module+"/"` from each key so the result matched the report's module-relative file
// names, and the module it was given came from gremlins' own report — which reads only the
// FIRST LINE of go.mod and TrimPrefixes "module " (gremlins internal/gomodule/gomodule.go).
// A comment on that line therefore becomes the module NAME: nothing is stripped, no key
// matches a report file name, and this cross-check — the gate's ONLY defence against a
// coverage map that disagrees with the profile the lane gathered itself — silently answers
// "nothing is covered" for every mutant in the repo. Measured 2026-09-27 on narcissus,
// where go.mod line 1 was a `go mod tidy -e` note: covered-unrun 0 while the lane's own
// cover step reported 92.9-98.2% on the very packages in question.
//
// [blocksFor] does the join by suffix instead, which needs no module name and cannot be
// fooled by one.
func coveredBlocks(profile string) map[string][][2]int {
	covered := map[string][][2]int{}
	for _, ln := range strings.Split(profile, "\n") {
		m := profileRow.FindStringSubmatch(strings.TrimSpace(ln))
		if m == nil {
			continue
		}
		// The digits matched, so these parse; a count of zero is a block that
		// never ran, which gremlins drops too.
		sl, _ := strconv.Atoi(m[2])
		sc, _ := strconv.Atoi(m[3])
		if count, _ := strconv.Atoi(m[4]); count == 0 {
			continue
		}
		covered[m[1]] = append(covered[m[1]], [2]int{sl, sc})
	}
	return covered
}

// startsOnLine reports whether the lane's profile has a covered block starting on this
// file's given line — the coarse question "did anything here run", as against
// [ScoreGoMutation]'s `misjudged`, which asks the exact switch-case signature.
//
// coveredBlocks keeps only block STARTS, so this cannot see a line in the middle of a
// multi-line block. That makes it a strict UNDER-count of the disagreements, which is the
// safe direction for what reads it: a guard that fires on "every uncovered mutant is
// disputed" must never fire on a partial view.
func startsOnLine(covered map[string][][2]int, name string, line int) bool {
	for _, b := range blocksFor(covered, name) {
		if b[0] == line {
			return true
		}
	}
	return false
}

// blocksFor answers the covered blocks for one module-relative file name.
//
// SUFFIX, NOT PREFIX-STRIPPING. A profile row names a file as `<import-path>/<relpath>`
// and gremlins' report names the same file as `<relpath>`, so a prefix strip needs the
// module path and is only as good as whoever parsed it. A relpath is unique within a
// module, so `key == name || strings.HasSuffix(key, "/"+name)` is exact and needs nothing
// but the two strings. The exact hit is tried first so a profile that already carries
// relative paths costs no scan.
func blocksFor(covered map[string][][2]int, name string) [][2]int {
	if b, ok := covered[name]; ok {
		return b
	}
	suffix := "/" + name
	for key, b := range covered {
		if strings.HasSuffix(key, suffix) {
			return b
		}
	}
	return nil
}

// pythonRound rounds half to even at the given decimals, as the scorer this
// replaces did, so a percentage reads the same number it always read.
func pythonRound(x float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.RoundToEven(x*p) / p
}

// GoMutationNoise is the classification mutation-gate -json answered: each
// survivor the testkit's AST walk named unkillable, keyed file:line:col, with
// its reason. nil means the classifier did not answer, which is not the same
// as "nothing was noise" — see GoMutationVerdict.
type GoMutationNoise map[string]string

// ParseGoMutationNoise reads mutation-gate -json's stdout.
//
// THE CLASSIFICATION HAS ONE HOME AND IT IS NOT HERE. forge-testkit-go's
// mutation package decides what is unkillable by parsing the source — a
// top-level const or var has no coverage block; a tagless switch's case
// expression sits before its block begins — and this scorer runs on the host,
// where the source is a dagger Directory and not a tree to parse. So the lane
// runs the testkit's gate INSIDE the container and hands its answer here,
// rather than this file carrying a copy of that AST walk to drift from it. The
// field names are the testkit's wire contract (Report's json tags), pinned by
// a test there.
func ParseGoMutationNoise(data []byte) (GoMutationNoise, error) {
	var r struct {
		Noise []struct {
			File   string `json:"file"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
			Reason string `json:"noise_reason"`
		} `json:"noise"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("the classification is not mutation-gate JSON: %v", err)
	}
	noise := GoMutationNoise{}
	for _, n := range r.Noise {
		noise[fmt.Sprintf("%s:%d:%d", n.File, n.Line, n.Column)] = n.Reason
	}
	return noise, nil
}

// gradedStatus is whether a status carries a claim about the tests. NOT VIABLE
// and SKIPPED carry none — gremlins decided them without running anything — so a
// runner that grades the wrong package does not make them wrong.
func gradedStatus(status string) bool {
	switch status {
	case "KILLED", "LIVED", "NOT COVERED", "TIMED OUT", "COVERED-UNRUN":
		return true
	}
	return false
}

// ScoreGoMutation scores gremlins' mutation-go.json against the coverage
// profile the lane gathered and the classification the testkit's gate
// answered. A nil noise forgives nothing; a nil misgraded distrusts nothing.
// goMutationAtom names what produced a mutation finding. This scorer reads
// gremlins, so it is always the Go lane's atom.
const goMutationAtom = "go:mutation"

// mutantFinding is one mutant as a finding.
//
// THE SUBJECT IS file:line, NOT file:line:col, matching the rest of this
// package's findings: a reader acts on the LINE — they write a test that reaches
// it — and `cause` already names which kind of change was made to it. The column
// rides in the detail, where it identifies the individual mutant without
// splitting one line's findings across several subjects, which is the opposite of
// what a grouping key is for.
func mutantFinding(verdict, file string, line, col int, cause, detail string) Finding {
	return Finding{
		Verdict: verdict,
		Subject: fmt.Sprintf("%s:%d", file, line),
		Cause:   cause,
		Detail:  fmt.Sprintf("%s, col %d", detail, col),
		Probe:   goMutationAtom,
	}
}

// mutantVerdict maps a mutant's status onto the schema's lattice.
//
// EVERY ROW IS THIS FILE'S OWN WORDS, not my reading of them:
//
//   - NOT COVERED is `violated` — the report itself calls it "the sharper of the
//     two: no test executes that code at all". That is a defect, not a maybe.
//   - LIVED is `drifted` — the report is emphatic that a survivor "is a
//     HYPOTHESIS, not a finding ... may be a real test gap or equivalent to the
//     original". `drifted` is the schema's word for exactly that: something moved,
//     look at it, no claim that it is broken. Calling it `violated` would assert
//     a defect the runner explicitly refuses to assert.
//   - COVERED-UNRUN is `unanalyzable` — this file's header says "not a miss, not
//     a kill, unmeasured by name", and unmeasured is what unanalyzable means.
//   - TIMED OUT is not here: it is TimedOutMutantIsDetected's to say.
//   - Anything else is `unanalyzable`, which is the schema's own degrade rule.
//     KILLED and the inert statuses never reach here.
func mutantVerdict(status string) string {
	switch status {
	case "NOT COVERED":
		return VerdictViolated
	case "LIVED":
		return VerdictDrifted
	default:
		return VerdictUnanalyzable
	}
}

func ScoreGoMutation(report []byte, profile, mode string, workers int, noise GoMutationNoise, misgraded GoMisgradedFiles) (GoMutationScore, error) {
	scored, elapsed, err := classifyGoReport(report, profile, noise, misgraded)
	if err != nil {
		return GoMutationScore{}, err
	}
	return scoreGoMutants(scored, GradingCounts{}, elapsed, mode, workers), nil
}

// classifyGoReport decides every mutant of a gomutants report: its status
// after the covered-unrun correction, and its outcome — ungraded, forgiven, or
// the status's own. These are the run's decisions; scoreGoMutants counts them.
func classifyGoReport(report []byte, profile string, noise GoMutationNoise, misgraded GoMisgradedFiles) ([]ScoredMutant, *float64, error) {
	var d struct {
		// GoModule IS DELIBERATELY UNREAD. gremlins fills it from its own go.mod
		// parse, which takes only the first line, so it is the one field in this
		// report the gate may not trust — see coveredBlocks. Kept because it
		// documents the wire shape, not because anything consumes it.
		GoModule    string   `json:"go_module"`
		ElapsedTime *float64 `json:"elapsed_time"`
		Files       []struct {
			FileName  string `json:"file_name"`
			Mutations []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
				Line   int    `json:"line"`
				Column int    `json:"column"`
			} `json:"mutations"`
		} `json:"files"`
	}
	if err := json.Unmarshal(report, &d); err != nil {
		return nil, nil, fmt.Errorf("the mutation report is not a gomutants report: %v", err)
	}
	covered := coveredBlocks(profile)
	misjudged := func(name string, line, col int) bool {
		for _, b := range blocksFor(covered, name) {
			if b[0] == line && b[1] > col {
				return true
			}
		}
		return false
	}
	var scored []ScoredMutant
	for _, f := range d.Files {
		for _, m := range f.Mutations {
			status := m.Status
			sm := ScoredMutant{File: f.FileName, Line: m.Line, Col: m.Column, Op: m.Type}
			if status == "NOT COVERED" {
				sm.Disputed = startsOnLine(covered, f.FileName, m.Line)
				if misjudged(f.FileName, m.Line, m.Column) {
					status = "COVERED-UNRUN"
				}
			}
			sm.Status = status
			reason, noisy := noise[fmt.Sprintf("%s:%d:%d", f.FileName, m.Line, m.Column)]
			switch {
			// THE FIRST QUESTION IS WHETHER THIS VERDICT IS ABOUT THIS CODE.
			// It is asked before forgiveness and before the counts because the
			// answer disqualifies both: the classifier can only speak about
			// survivors, so a FALSE KILL in a main package is a hole no
			// forgiveness can reach — and it is the direction that reads green.
			case misgraded[f.FileName] && gradedStatus(status):
				sm.Outcome = OutcomeUngraded
			// Only a survivor is a candidate: the testkit classifies NOT
			// COVERED alone, and a mutant that LIVED was executed by a test
			// that failed to notice it — a test gap by definition, never
			// forgiven. The key is asked before the status is counted so a
			// forgiven mutant lands in exactly one column.
			case noisy && (status == "NOT COVERED" || status == "LIVED"):
				sm.Outcome, sm.Detail = OutcomeForgiven, reason
			default:
				sm.Outcome = goOutcomes[status]
			}
			scored = append(scored, sm)
		}
	}
	return scored, d.ElapsedTime, nil
}

// scoreGoMutants counts decided mutants into the gate's score: the counts,
// the survivor lists, the findings and the summary. extra are the mutants a
// reused grading carries as counts alone (killed and inert). The mutants are
// read in position order, so a run that graded every unit and a run that
// reused some answer the same findings in the same order.
func scoreGoMutants(scored []ScoredMutant, extra GradingCounts, elapsed *float64, mode string, workers int) GoMutationScore {
	workers = max(1, workers)
	sorted := slices.Clone(scored)
	slices.SortStableFunc(sorted, compareMutants)
	s := GoMutationScore{Scored: sorted, Generated: len(sorted) + extra.Generated, Killed: extra.Killed, Inert: extra.Inert}
	var timed, misread []string
	for _, m := range sorted {
		where := fmt.Sprintf("%s:%d:%d  %-13s %s", m.File, m.Line, m.Col, m.Status, m.Op)
		if m.Disputed {
			s.ProfileDisagrees++
		}
		switch m.Outcome {
		case OutcomeUngraded:
			s.Ungraded = append(s.Ungraded, where)
			// UNANALYZABLE WHATEVER THE STATUS SAID, including a KILLED —
			// this is the one place a kill becomes a finding, because the
			// verdict is about a different package and "the gate cannot
			// trust it" is a fact a reader needs, not a pass.
			s.Findings = append(s.Findings, mutantFinding(VerdictUnanalyzable,
				m.File, m.Line, m.Col, m.Op,
				m.Status+" but graded against the wrong package (gremlins#268)"))
		case OutcomeForgiven:
			s.Forgiven = append(s.Forgiven, fmt.Sprintf("%s  [%s]", where, m.Detail))
			// THE CAUSE IS THE FORGIVENESS, NOT THE OPERATOR. For an
			// `excluded` finding the schema requires a cause and means it to
			// answer "by what", and the reader's question about a forgiven
			// mutant is which class excused it — `3 × unkillable-declaration`
			// is the line they act on. The operator moves to the detail.
			s.Findings = append(s.Findings, mutantFinding(VerdictExcluded,
				m.File, m.Line, m.Col, m.Detail, m.Status+" but forgiven, "+m.Op))
		case OutcomeKilled:
			s.Killed++
		case OutcomeLived:
			s.Lived++
			s.Missed = append(s.Missed, where)
			s.Findings = append(s.Findings, mutantFinding(mutantVerdict(m.Status), m.File, m.Line, m.Col, m.Op, m.Status))
		case OutcomeNotCovered:
			s.NotCovered++
			s.Missed = append(s.Missed, where)
			s.Findings = append(s.Findings, mutantFinding(mutantVerdict(m.Status), m.File, m.Line, m.Col, m.Op, m.Status))
		case OutcomeTimedOut:
			// ONE TIMEOUT DECISION FOR EVERY LANE (TimedOutMutantIsDetected):
			// its word and its cause are the Rust lane's, the operator moves
			// to the detail, and GoMutationTimeoutBudget stays this lane's
			// guard against a run that is mostly hangs.
			s.TimedOut++
			timed = append(timed, where)
			s.Findings = append(s.Findings, mutantFinding(timedOutMutantVerdict(TimedOutMutantIsDetected),
				m.File, m.Line, m.Col, MutantTimeoutCause, m.Status+" "+m.Op+" — "+MutantTimeoutAdvice))
		case OutcomeCoveredUnrun:
			s.CoveredUnrun++
			misread = append(misread, where)
			s.Findings = append(s.Findings, mutantFinding(mutantVerdict(m.Status), m.File, m.Line, m.Col, m.Op, m.Status))
		case OutcomeInert:
			// NOTHING TO CHECK, so nothing to report. `inert` exists in the
			// lattice for this, but a finding per inert mutant would be 2,763
			// of them on a real run — the counts carry it.
			s.Inert++
		}
	}
	viable := s.Viable()
	pct := 0.0
	if viable > 0 {
		pct = pythonRound(float64(s.Killed)/float64(viable)*100, 0)
	}
	if s.Generated > 0 {
		s.TimedOutPct = pythonRound(float64(s.TimedOut)/float64(s.Generated)*100, 1)
	}
	s.MsPerMutant = -1
	if ran := s.Killed + s.Lived + s.TimedOut; elapsed != nil && ran > 0 {
		s.MsPerMutant = *elapsed * 1000 * float64(workers) / float64(ran)
	}

	out := []string{
		fmt.Sprintf("### Mutation gate — go (%s)", mode), "",
		"| killed | lived | not-covered | forgiven | timed out | covered-unrun | ungraded | inert | kill rate |",
		"|---|---|---|---|---|---|---|---|---|",
		fmt.Sprintf("| %d | %d | %d | %d | %d (%s%%) | %d | %d | %d | %.0f%% of %d viable |",
			s.Killed, s.Lived, s.NotCovered, len(s.Forgiven), s.TimedOut, strconv.FormatFloat(s.TimedOutPct, 'f', -1, 64), s.CoveredUnrun, len(s.Ungraded), s.Inert, pct, viable),
		"",
	}
	if len(s.Forgiven) > 0 {
		// A FORGIVENESS NOBODY CAN READ IS A SUPPRESSION. Every excused mutant
		// is listed with the class that excused it, above the survivors, so a
		// reader deciding whether the pass was earned sees what was set aside.
		out = append(out,
			fmt.Sprintf("**%d mutant(s) forgiven — unkillable by construction, not untested.**", len(s.Forgiven)),
			"Classified by forge-testkit-go's mutation gate from the source: a",
			"top-level const or var has no coverage block, and a tagless switch's",
			"case expression sits before its block begins. No test could kill these.", "", "```")
		out = append(append(out, s.Forgiven...), "```", "")
	}
	if len(s.Ungraded) > 0 {
		// FORGIVEN AND UNGRADED ARE OPPOSITE CLAIMS and must never share a
		// column. Forgiven says "no test could ever kill this"; ungraded says
		// "this runner did not look at it". One is about the code, the other
		// about the tool.
		out = append(out,
			fmt.Sprintf("**%d mutant(s) were NOT GRADED — a `package main` below the module root.**", len(s.Ungraded)),
			"This gremlins resolves a file's package from its PACKAGE CLAUSE, so such a",
			"file resolves to the MODULE ROOT, and the tests it runs for the mutant are",
			"not the tests that cover it. With no package at the root the run fails and",
			"the mutant reads KILLED (a FALSE GREEN); with one, that package's tests",
			"pass and the mutant reads LIVED (a false red). A main package that IS the",
			"root resolves to itself and is graded correctly, so it is not in here.",
			"Upstream gremlins#268, fix open at #306. Excluded from the rate and listed",
			"here, because a hole in the measurement is not a pass. The package-main",
			"control decides this — when a gremlins that grades a main package lands it",
			"answers OK and these mutants count again, with nobody editing this file.", "", "```")
		out = append(append(out, s.Ungraded...), "```", "")
	}
	if s.MsPerMutant >= 0 {
		out = append(out,
			fmt.Sprintf("_%.0fms of wall clock per mutant across %d worker(s)._", s.MsPerMutant, workers),
			"_Informational. The control that decides whether the mutants really_",
			"_ran is the canary, not this number._", "")
	}
	if s.Generated > 0 && viable == 0 {
		why := []string{"It did not verify anything. In diff mode no CHANGED LINE carried a",
			"mutable operator (gremlins' --diff is line-ranged)."}
		if len(s.Ungraded) > 0 {
			why = []string{"It did not verify anything. Every mutant it could have graded sits",
				"in a `package main` below the root, graded against the wrong package."}
		}
		out = append(out, fmt.Sprintf("**This run generated %d mutant(s) and measured NONE of them.**", s.Generated))
		out = append(append(out, why...), "")
	}
	if len(misread) > 0 {
		out = append(out,
			fmt.Sprintf("**%d mutant(s) sit on code the coverage profile says RUNS,**", len(misread)),
			"and gremlins still reported them NOT COVERED (Go's cover tool starts a",
			"`switch` case block after the case expression). Not misses, not kills:",
			"unmeasured.", "", "```")
		out = append(append(out, misread...), "```", "")
	}
	if len(timed) > 0 {
		out = append(out,
			fmt.Sprintf("**%d mutant(s) TIMED OUT — neither killed nor survived.**", len(timed)),
			"gremlins drops them from its own score; this gate lists each as a",
			"`mutant-timeout` finding, and over the timeout budget the suite was",
			"not measured.", "")
	}
	if len(s.Missed) > 0 {
		out = append(out,
			"**Survivors** — each is a HYPOTHESIS, not a finding. A mutant that",
			"lives may be a real test gap or equivalent to the original. NOT COVERED",
			"is the sharper of the two: no test executes that code at all.", "", "```")
		out = append(append(out, s.Missed...), "```")
	}
	s.Summary = strings.Join(out, "\n") + "\n"
	return s
}

// goOutcomes maps a status the scorer counts onto its grading outcome. A
// status it does not know maps to "", counted as generated and nothing else.
var goOutcomes = map[string]string{
	"KILLED": OutcomeKilled, "LIVED": OutcomeLived, "NOT COVERED": OutcomeNotCovered,
	"TIMED OUT": OutcomeTimedOut, "COVERED-UNRUN": OutcomeCoveredUnrun,
	"NOT VIABLE": OutcomeInert, "SKIPPED": OutcomeInert,
}

// GoMutationTimeoutBudget is the percent of timed-out mutants over which a run
// did not measure the suite.
const GoMutationTimeoutBudget = 10.0

// GoMutationOverBudgetCause is the phrase an over-budget run's reason carries
// between the percentage and the budget. It is how a caller tells this
// could-not-run — a deterministic result of the tree and the lane's config —
// from a transient one (verdictFor's memo of it, foundry-tools#16081).
const GoMutationOverBudgetCause = "of mutants TIMED OUT, over the"

// GoMutationOverBudget reports whether a go:mutation verdict is the
// over-budget timeout could-not-run: the same tree under the same config times
// out the same way, so asking again can only repeat it.
func GoMutationOverBudget(state int, reason string) bool {
	return state == int(StateCannotRun) && strings.Contains(reason, GoMutationOverBudgetCause)
}

// GoMutationNothingGraded opens the reason of a run that produced verdicts and
// could trust NONE of them. It settles 0 — there is no test gap to point at and
// no committer who can fix gremlins#268 — so this is the one green whose reason
// must reach a reader anyway: VerdictOf discards a PASSING atom's output from
// Reason (it survives in Logs), and a lane line reading `go:mutation - pass` is
// exactly the misreading this whole change exists to remove. The atom lifts the
// reason's first line past that discard; see goMutationIn's settle.
const GoMutationNothingGraded = "NOTHING WAS GRADED"

// GoMutationRun is what the lane measured, for the verdict.
type GoMutationRun struct {
	// Status is gremlins' exit code.
	Status int
	// Log is the run's combined output, and it is here because a run that could
	// not measure has to say WHY without a transcript dig. Its Rust and
	// TypeScript siblings have carried theirs all along (RustMutationRun.Log,
	// StrykerRun.Log); this one did not, and the cost was measured — see the
	// no-report branch of GoMutationVerdict.
	Log string
	// Report is mutation-go.json; empty when gremlins wrote none.
	Report []byte
	// Profile is the coverage profile; empty when none was gathered.
	Profile string
	// Canary is the harness control's answer: CanaryOK, CanaryBroken or
	// CanaryUnknown. Broken is fatal — see GoMutationVerdict.
	Canary string
	// MainCanary is the package-main control's answer. CanaryOK means this
	// gremlins grades a main package correctly and MisgradedFiles is ignored;
	// anything else means it does not, and those files are excluded. Unknown
	// distrusts: a control that could not be read has not cleared the runner.
	MainCanary string
	// MisgradedFiles are the module-relative files this runner grades against
	// the wrong package, as ParseGoMisgradedFiles read them. MisgradedFilesErr
	// is why the lane could not list them, when it could not.
	MisgradedFiles    GoMisgradedFiles
	MisgradedFilesErr string
	Workers           int
	// Classified is what mutation-gate -json wrote in the lane; empty when it
	// did not answer. ClassifyErr is what it said on stderr when it did not.
	Classified  []byte
	ClassifyErr string
}

// GoMutationVerdict settles a diff-mode run: 0 clean, 1 survivors, 2 did not
// measure. The reason carries the summary whenever a report was scored.
func GoMutationVerdict(run GoMutationRun) (int, string, []Finding) {
	state, reason, found, _ := GoMutationVerdictScored(run)
	return state, reason, found
}

// GoMutationVerdictScored is GoMutationVerdict with the score it settled
// from — nil when no report was scored — which the atom builds its gradings
// from (gradings.go).
func GoMutationVerdictScored(run GoMutationRun) (int, string, []Finding, *GoMutationScore) {
	f := GoMutationVerdictReusing(run, ".", nil)
	return f.State, f.Reason, f.Findings, f.Score
}

// GoFold is a Go mutation run's verdict over the units it graded and the
// gradings it reused: the state, reason and findings a run that graded every
// unit would have answered, the score over all of them, and the mutants this
// run graded itself (module-relative), which are all its gradings may store.
type GoFold struct {
	State    int
	Reason   string
	Findings []Finding
	Score    *GoMutationScore
	Fresh    []ScoredMutant
}

// GoMutationVerdictReusing settles a run that graded only the units no stored
// grading answered, folding the reused gradings in (dir is the module's
// directory; reused mutants are repository-relative). The fold is one score
// over every mutant, so every count, every guard and every finding is the one
// a cold run makes — which is the contract: a reuse run's verdict equals a
// cold run's. With nothing reused it is GoMutationVerdictScored exactly.
func GoMutationVerdictReusing(run GoMutationRun, dir string, reused []ReusedGrading) GoFold {
	if len(run.Report) == 0 && (len(reused) == 0 || run.Status != 0) {
		if run.Status == 0 {
			// gremlins writes no report when it has nothing to report and exits
			// 0: the pull touched no mutable Go in scope. Clean, and said to have
			// measured nothing.
			return GoFold{State: 0, Reason: "gomutants had no results to report (exit 0, no report): the pull touched no mutable Go code in scope"}
		}
		// AND IT SAYS WHY, because the exit code alone sent a reader to the
		// archive. MEASURED on ourea@59b3a39 (2026-09-26): this branch settled
		// the lane could-not-run with `gremlins exited 1 and wrote no
		// mutation-go.json — nothing was measured`, twice, and the actual cause
		// was one flaky unit test — gremlins gathers baseline coverage with
		// `go test`, a failing package makes that exit 1, and it aborts before
		// writing the report. The cause was in the pod transcript the whole time:
		//
		//     --- FAIL: TestThePollerLingersAndThenStops
		//     citail_test.go:288: the poller stopped capturing while idle (3 -> 3)
		//     ERROR: failed to gather coverage: impossible to executeCoverage
		//
		// Finding that took reading 539 archived lines for a fact the settle
		// could have carried in twenty. The Rust and TypeScript siblings already
		// tail their log here; this one now does too.
		return GoFold{State: 2, Reason: fmt.Sprintf("gomutants exited %d and wrote no mutation-go.json — nothing was measured\n%s",
			run.Status, tail(run.Log, 20))}
	}
	// THE CLASSIFIER NOT ANSWERING IS A FACT THE VERDICT MUST CARRY. Scored
	// without it, every unkillable declaration reads as a survivor and a
	// clean pull is red for a reason no test can fix; scored as if it had
	// answered "nothing", the same. So a missing classification forgives
	// nothing here, and below it turns a survivor verdict into "could not
	// measure" — the survivors are real or noise and this run cannot say.
	//
	// A FOLD WITH NO FRESH REPORT HAS NOTHING OF ITS OWN TO CLASSIFY: every
	// survivor it holds was classified in the run that graded it, and only a
	// trusted run's gradings are reused. So the classification is present and
	// empty, never "did not answer".
	var noise GoMutationNoise
	classifyErr := strings.TrimSpace(run.ClassifyErr)
	if len(run.Report) == 0 {
		noise, classifyErr = GoMutationNoise{}, ""
	} else if len(bytes.TrimSpace(run.Classified)) > 0 {
		var err error
		if noise, err = ParseGoMutationNoise(run.Classified); err != nil {
			classifyErr = err.Error()
		}
	} else if classifyErr == "" {
		classifyErr = "mutation-gate wrote nothing"
	}
	// THE PACKAGE-MAIN EXCLUSION IS KEYED ON ITS CONTROL, so it expires by
	// MEASUREMENT rather than by somebody remembering to come back. A broken
	// package-main control is NOT fatal the way a broken harness control is: it
	// is the expected answer on gremlins 0.6.0, and reddening on it would red
	// every pull in the 36 of 38 fleet repos that have no root package, over an
	// upstream bug no committer here can fix. So it switches the exclusion on
	// instead — and switches it off by itself the day a fixed gremlins lands.
	misgraded := run.MisgradedFiles
	if run.MainCanary == CanaryOK {
		misgraded = nil
	}
	var fresh []ScoredMutant
	var elapsed *float64
	if len(run.Report) > 0 {
		var err error
		if fresh, elapsed, err = classifyGoReport(run.Report, run.Profile, noise, misgraded); err != nil {
			return GoFold{State: 2, Reason: "the mutation report could not be read: " + err.Error()}
		}
	}
	listed, extra := reusedInModule(reused, dir)
	s := scoreGoMutants(append(slices.Clone(fresh), listed...), extra, elapsed, "diff", run.Workers)
	state, reason, found := goScoredVerdict(run, s, noise, classifyErr)
	if len(reused) > 0 {
		reason += "\n" + ReusedSection(reused)
	}
	return GoFold{State: state, Reason: reason, Findings: found, Score: &s, Fresh: fresh}
}

// goScoredVerdict settles a run whose report scored: its exit, the timeout
// budget, the controls, then the survivors.
func goScoredVerdict(run GoMutationRun, s GoMutationScore, noise GoMutationNoise, classifyErr string) (int, string, []Finding) {
	missed := len(s.Missed)
	measured := fmt.Sprintf("measured: %d missed, %s%% timed out", missed, strconv.FormatFloat(s.TimedOutPct, 'f', -1, 64))
	if s.MsPerMutant >= 0 {
		measured += fmt.Sprintf(", %.1fms of wall clock per mutant", s.MsPerMutant)
	}
	with := func(line string) string { return line + "\n" + measured + "\n\n" + s.Summary }
	if run.Status != 0 {
		return 2, with(fmt.Sprintf("gomutants exited %d — a broken run, not a survivor report", run.Status)), nil
	}
	if s.TimedOutPct > GoMutationTimeoutBudget {
		return 2, with(fmt.Sprintf("%s%% "+GoMutationOverBudgetCause+" %.0f%% budget — the suite was not measured", strconv.FormatFloat(s.TimedOutPct, 'f', -1, 64), GoMutationTimeoutBudget)), nil
	}
	if run.Canary == CanaryBroken {
		// Fatal like a timeout-heavy run: neither measured anything.
		return 2, with("the harness scores unrun tests as kills: the control mutant, which must SURVIVE, came back KILLED — every kill in this report is false. See #7649"), nil
	}
	if run.MainCanary != CanaryOK && run.MisgradedFilesErr != "" {
		// NOTHING TO EXCLUDE WITH. This gremlins grades a main package against
		// the module root and the lane could not say which files are in one, so
		// the counts below may include verdicts about other code entirely — and
		// the gate cannot point at which. That is a could-not-measure, not a pass.
		return 2, with("this gremlins grades a `package main` against the module root (gremlins#268) and the lane could not list which files are in one: " + run.MisgradedFilesErr), nil
	}
	// NOTHING WAS GRADED AGAINST THE TESTS, and the profile says so. A run that
	// killed nothing, lost nothing, and whose EVERY uncovered mutant sits on a line
	// the lane's own `go test -cover` reported as running did not measure the tests
	// — its coverage map is empty relative to ours. That is could-not-measure, not
	// a wall of survivors: by findings.schema.json `unanalyzable` is "could not
	// run, EVIDENCE OF NOTHING" while `violated` is "ran; found a defect", and
	// reporting the second for the first is the conflation that shape exists to kill.
	//
	// MEASURED 2026-09-27 on narcissus#105/#106: `Mutator coverage: 0.00%`, killed 0,
	// lived 0, every added line NOT COVERED — over packages the lane's own cover step
	// had just measured at 92.9-98.2%. The cause was a comment on go.mod line 1,
	// which gremlins reads as the module name (see coveredBlocks); the lane reported
	// 14 violations and cost three wrong diagnoses before anyone read the parser.
	//
	// IT CANNOT SUPPRESS A REAL TEST GAP, and that is what the profile term buys. A
	// pull that genuinely adds untested code is also killed 0 / lived 0 — but the
	// lane's profile has NO covered block on those lines, so ProfileDisagrees stays
	// below NotCovered and this does not fire. The equality is the whole guard:
	// every single uncovered mutant must be disputed, not merely some.
	if s.Killed == 0 && s.Lived == 0 && s.NotCovered > 0 && s.ProfileDisagrees == s.NotCovered {
		return 2, with(fmt.Sprintf(
			"%s — killed 0, lived 0, and all %d NOT COVERED mutant(s) sit on lines this lane's own coverage profile reports as running. gremlins' coverage map is empty relative to the profile gathered beside it, so nothing here was measured against the tests. Check that go.mod's FIRST line is `module ...` — gremlins reads only that line",
			GoMutationNothingGraded, s.NotCovered)), nil
	}
	if missed == 0 {
		if len(s.Ungraded) > 0 && s.Viable() == 0 {
			// Clean, and clean ABOUT it: exit 0, because there is no test gap to
			// point at and no committer who can fix #268 — and a reason that says
			// in as many words that this run verified nothing.
			return 0, with(fmt.Sprintf("%s — all %d mutant(s) with a verdict sit in a `package main` below the module root, which this gremlins grades against the root instead (gremlins#268). This run did not verify the tests", GoMutationNothingGraded, len(s.Ungraded))), nil
		}
		return 0, with("every viable mutant was caught"), nil
	}
	if noise == nil {
		return 2, with(fmt.Sprintf("%d mutant(s) survived and the unkillability classifier did not answer (%s) — this run cannot tell a test gap from a declaration no test could reach", missed, classifyErr)), nil
	}
	// CAPPED LIKE EVERY OTHER ATOM'S. These findings do not come through
	// FindingsOf, so they do not get capFindings for free — and a wide pull can
	// generate hundreds of survivors, which is exactly the runaway the cap exists
	// to stop: the record travels on ONE line of stdout. The cut states itself as
	// an `excluded` finding and the full list is in the summary either way.
	return 1, with(fmt.Sprintf("%d mutant(s) survived or were never covered — see the list below", missed)), capFindings(s.Findings, goMutationAtom)
}
