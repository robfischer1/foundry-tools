package checks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
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

// coveredBlocks reads a `go test -coverprofile` into the start (line, column)
// of every block that ran, by file relative to the module.
func coveredBlocks(profile, module string) map[string][][2]int {
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
		name := m[1]
		if module != "" {
			name = strings.TrimPrefix(name, module+"/")
		}
		covered[name] = append(covered[name], [2]int{sl, sc})
	}
	return covered
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
func ScoreGoMutation(report []byte, profile, mode string, workers int, noise GoMutationNoise, misgraded GoMisgradedFiles) (GoMutationScore, error) {
	var d struct {
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
		return GoMutationScore{}, fmt.Errorf("the mutation report is not gremlins JSON: %v", err)
	}
	workers = max(1, workers)
	covered := coveredBlocks(profile, d.GoModule)
	misjudged := func(name string, line, col int) bool {
		for _, b := range covered[name] {
			if b[0] == line && b[1] > col {
				return true
			}
		}
		return false
	}

	var s GoMutationScore
	var timed, misread []string
	for _, f := range d.Files {
		for _, m := range f.Mutations {
			status := m.Status
			if status == "NOT COVERED" && misjudged(f.FileName, m.Line, m.Column) {
				status = "COVERED-UNRUN"
			}
			s.Generated++
			where := fmt.Sprintf("%s:%d:%d  %-13s %s", f.FileName, m.Line, m.Column, status, m.Type)
			// THE FIRST QUESTION IS WHETHER THIS VERDICT IS ABOUT THIS CODE.
			// It is asked before forgiveness and before the counts because the
			// answer disqualifies both: the classifier can only speak about
			// survivors, so a FALSE KILL in a main package is a hole no
			// forgiveness can reach — and it is the direction that reads green.
			if misgraded[f.FileName] && gradedStatus(status) {
				s.Ungraded = append(s.Ungraded, where)
				continue
			}
			// Only a survivor is a candidate: the testkit classifies NOT
			// COVERED alone, and a mutant that LIVED was executed by a test
			// that failed to notice it — a test gap by definition, never
			// forgiven. The key is asked before the status is counted so a
			// forgiven mutant lands in exactly one column.
			if reason, ok := noise[fmt.Sprintf("%s:%d:%d", f.FileName, m.Line, m.Column)]; ok && (status == "NOT COVERED" || status == "LIVED") {
				s.Forgiven = append(s.Forgiven, fmt.Sprintf("%s  [%s]", where, reason))
				continue
			}
			switch status {
			case "KILLED":
				s.Killed++
			case "LIVED":
				s.Lived++
				s.Missed = append(s.Missed, where)
			case "NOT COVERED":
				s.NotCovered++
				s.Missed = append(s.Missed, where)
			case "TIMED OUT":
				s.TimedOut++
				timed = append(timed, where)
			case "COVERED-UNRUN":
				s.CoveredUnrun++
				misread = append(misread, where)
			case "NOT VIABLE", "SKIPPED":
				s.Inert++
			}
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
	if ran := s.Killed + s.Lived + s.TimedOut; d.ElapsedTime != nil && ran > 0 {
		s.MsPerMutant = *d.ElapsedTime * 1000 * float64(workers) / float64(ran)
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
			"A timeout is an UNMEASURED mutant; gremlins drops them from its own",
			"score, this gate counts them.", "")
	}
	if len(s.Missed) > 0 {
		out = append(out,
			"**Survivors** — each is a HYPOTHESIS, not a finding. A mutant that",
			"lives may be a real test gap or equivalent to the original. NOT COVERED",
			"is the sharper of the two: no test executes that code at all.", "", "```")
		out = append(append(out, s.Missed...), "```")
	}
	s.Summary = strings.Join(out, "\n") + "\n"
	return s, nil
}

// GoMutationTimeoutBudget is the percent of timed-out mutants over which a run
// did not measure the suite.
const GoMutationTimeoutBudget = 10.0

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
func GoMutationVerdict(run GoMutationRun) (int, string) {
	if len(run.Report) == 0 {
		if run.Status == 0 {
			// gremlins writes no report when it has nothing to report and exits
			// 0: the pull touched no mutable Go in scope. Clean, and said to have
			// measured nothing.
			return 0, "gremlins had no results to report (exit 0, no report): the pull touched no mutable Go code in scope"
		}
		return 2, fmt.Sprintf("gremlins exited %d and wrote no mutation-go.json — nothing was measured", run.Status)
	}
	// THE CLASSIFIER NOT ANSWERING IS A FACT THE VERDICT MUST CARRY. Scored
	// without it, every unkillable declaration reads as a survivor and a
	// clean pull is red for a reason no test can fix; scored as if it had
	// answered "nothing", the same. So a missing classification forgives
	// nothing here, and below it turns a survivor verdict into "could not
	// measure" — the survivors are real or noise and this run cannot say.
	var noise GoMutationNoise
	classifyErr := strings.TrimSpace(run.ClassifyErr)
	if len(bytes.TrimSpace(run.Classified)) > 0 {
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
	s, err := ScoreGoMutation(run.Report, run.Profile, "diff", run.Workers, noise, misgraded)
	if err != nil {
		return 2, "the mutation report could not be read: " + err.Error()
	}
	missed := len(s.Missed)
	measured := fmt.Sprintf("measured: %d missed, %s%% timed out", missed, strconv.FormatFloat(s.TimedOutPct, 'f', -1, 64))
	if s.MsPerMutant >= 0 {
		measured += fmt.Sprintf(", %.1fms of wall clock per mutant", s.MsPerMutant)
	}
	with := func(line string) string { return line + "\n" + measured + "\n\n" + s.Summary }
	if run.Status != 0 {
		return 2, with(fmt.Sprintf("gremlins exited %d — a broken run, not a survivor report", run.Status))
	}
	if s.TimedOutPct > GoMutationTimeoutBudget {
		return 2, with(fmt.Sprintf("%s%% of mutants TIMED OUT, over the %.0f%% budget — the suite was not measured", strconv.FormatFloat(s.TimedOutPct, 'f', -1, 64), GoMutationTimeoutBudget))
	}
	if run.Canary == CanaryBroken {
		// Fatal like a timeout-heavy run: neither measured anything.
		return 2, with("the harness scores unrun tests as kills: the control mutant, which must SURVIVE, came back KILLED — every kill in this report is false. See #7649")
	}
	if run.MainCanary != CanaryOK && run.MisgradedFilesErr != "" {
		// NOTHING TO EXCLUDE WITH. This gremlins grades a main package against
		// the module root and the lane could not say which files are in one, so
		// the counts below may include verdicts about other code entirely — and
		// the gate cannot point at which. That is a could-not-measure, not a pass.
		return 2, with("this gremlins grades a `package main` against the module root (gremlins#268) and the lane could not list which files are in one: " + run.MisgradedFilesErr)
	}
	if missed == 0 {
		if len(s.Ungraded) > 0 && s.Viable() == 0 {
			// Clean, and clean ABOUT it: exit 0, because there is no test gap to
			// point at and no committer who can fix #268 — and a reason that says
			// in as many words that this run verified nothing.
			return 0, with(fmt.Sprintf("%s — all %d mutant(s) with a verdict sit in a `package main` below the module root, which this gremlins grades against the root instead (gremlins#268). This run did not verify the tests", GoMutationNothingGraded, len(s.Ungraded)))
		}
		return 0, with("every viable mutant was caught")
	}
	if noise == nil {
		return 2, with(fmt.Sprintf("%d mutant(s) survived and the unkillability classifier did not answer (%s) — this run cannot tell a test gap from a declaration no test could reach", missed, classifyErr))
	}
	return 1, with(fmt.Sprintf("%d mutant(s) survived or were never covered — see the list below", missed))
}
