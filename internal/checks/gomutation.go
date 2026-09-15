package checks

import (
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
// AND A RUN CAN REPORT KILLS IT NEVER EARNED. gremlins' oracle is the test
// process's exit code, so a mutant whose test never started reads as a KILL and
// a broken harness scores perfectly (gremlins v0.6.0's --test-cpu, #7649: 264
// "kills" in 262ms). No wall-clock floor detects that — a failing `go test`
// costs ~1ms in CI and ~250ms on a host re-execing a toolchain — so the control
// is a canary whose honest verdict is LIVED (GoMutationCanary). ms per mutant is
// reported for a reader and gates nothing.

// GoMutationScore is a gremlins report scored the way the gate reads it.
type GoMutationScore struct {
	Killed, Lived, NotCovered, TimedOut, CoveredUnrun, Inert, Generated int
	// Missed are the LIVED and NOT COVERED mutants, as file:line:col status type.
	Missed []string
	// TimedOutPct is timed-out mutants over every mutant generated.
	TimedOutPct float64
	// MsPerMutant is wall clock per mutant that ran, times the workers; -1 when
	// the report carries no elapsed time or nothing ran.
	MsPerMutant float64
	// Summary is the human- and agent-readable account.
	Summary string
}

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

// ScoreGoMutation scores gremlins' mutation-go.json against the coverage
// profile the lane gathered.
func ScoreGoMutation(report []byte, profile, mode string, workers int) (GoMutationScore, error) {
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
	viable := s.Killed + s.Lived + s.NotCovered
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
		"| killed | lived | not-covered | timed out | covered-unrun | inert | kill rate |",
		"|---|---|---|---|---|---|---|",
		fmt.Sprintf("| %d | %d | %d | %d (%s%%) | %d | %d | %.0f%% of %d viable |",
			s.Killed, s.Lived, s.NotCovered, s.TimedOut, strconv.FormatFloat(s.TimedOutPct, 'f', -1, 64), s.CoveredUnrun, s.Inert, pct, viable),
		"",
	}
	if s.MsPerMutant >= 0 {
		out = append(out,
			fmt.Sprintf("_%.0fms of wall clock per mutant across %d worker(s)._", s.MsPerMutant, workers),
			"_Informational. The control that decides whether the mutants really_",
			"_ran is the canary, not this number._", "")
	}
	if s.Generated > 0 && viable == 0 {
		out = append(out,
			fmt.Sprintf("**This run generated %d mutant(s) and measured NONE of them.**", s.Generated),
			"It did not verify anything. In diff mode no CHANGED LINE carried a",
			"mutable operator (gremlins' --diff is line-ranged).", "")
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

// GoMutationRun is what the lane measured, for the verdict.
type GoMutationRun struct {
	// Status is gremlins' exit code.
	Status int
	// Report is mutation-go.json; empty when gremlins wrote none.
	Report []byte
	// Profile is the coverage profile; empty when none was gathered.
	Profile string
	// Canary is the control's answer: CanaryOK, CanaryBroken or CanaryUnknown.
	Canary  string
	Workers int
}

// The canary's answers.
const (
	CanaryOK      = "ok"
	CanaryBroken  = "broken"
	CanaryUnknown = "unknown"
)

// GoMutationCanary reads the control run's gremlins output. The control's
// honest verdict is one LIVED mutant: a harness that runs its tests answers
// Killed 0 / Lived 1, one that cannot start the child answers Killed 1, and
// anything else could not be read.
func GoMutationCanary(out string) string {
	switch {
	case strings.Contains(out, "Killed: 0, Lived: 1"):
		return CanaryOK
	case strings.Contains(out, "Lived: 0"):
		return CanaryBroken
	}
	return CanaryUnknown
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
	s, err := ScoreGoMutation(run.Report, run.Profile, "diff", run.Workers)
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
	if missed == 0 {
		return 0, with("every viable mutant was caught")
	}
	return 1, with(fmt.Sprintf("%d mutant(s) survived or were never covered — see the list below", missed))
}

// The canary module, written into the lane's container from here: a mutant
// whose honest verdict is LIVED. Add's `+` is mutable and its test runs it
// without asserting the result, so an arithmetic mutation changes no outcome.
// Keep it dependency-free — a canary that cannot build is a control that
// cannot control.
const (
	GoMutationCanaryMod  = "module canary\n\ngo 1.21\n"
	GoMutationCanaryCode = "package canary\n\n// Add is mutable and deliberately under-tested: its mutant must survive.\nfunc Add(a, b int) int { return a + b }\n"
	GoMutationCanaryTest = "package canary\n\nimport \"testing\"\n\n// TestAddRuns covers Add without checking it, on purpose.\nfunc TestAddRuns(t *testing.T) {\n\tgot := Add(2, 3)\n\tt.Logf(\"Add(2, 3) = %d\", got)\n}\n"
)
