package checks

import (
	"reflect"
	"strings"
	"testing"
)

// goReport is a gremlins report over one file carrying every status.
const goReport = `{"go_module":"example.com/x","elapsed_time":2.5,"files":[{"file_name":"ops/ops.go","mutations":[
{"type":"CONDITIONALS_NEGATION","status":"KILLED","line":10,"column":5},
{"type":"CONDITIONALS_NEGATION","status":"KILLED","line":11,"column":5},
{"type":"ARITHMETIC_BASE","status":"LIVED","line":12,"column":9},
{"type":"CONDITIONALS_BOUNDARY","status":"NOT COVERED","line":20,"column":7},
{"type":"CONDITIONALS_NEGATION","status":"NOT COVERED","line":30,"column":7},
{"type":"INCREMENT_DECREMENT","status":"TIMED OUT","line":40,"column":3},
{"type":"INVERT_NEGATIVES","status":"NOT VIABLE","line":50,"column":3},
{"type":"INVERT_LOOPCTRL","status":"SKIPPED","line":51,"column":3}]}]}`

// goProfile covers ops.go: line 30 carries a block that STARTS to the right of
// the mutant at column 7 (a switch case after its expression); line 20's block
// starts to its left, and a zero-count block on line 12 is not coverage.
const goProfile = "mode: set\n" +
	"example.com/x/ops/ops.go:30.12,31.2 1 1\n" +
	"example.com/x/ops/ops.go:20.2,21.2 1 1\n" +
	"example.com/x/ops/ops.go:12.20,13.2 1 0\n" +
	"not a row\n" +
	"example.com/x/ops/ops.go:bad 1 1\n"

func TestScoreGoMutationCountsEveryStatusFromTheMutations(t *testing.T) {
	s, err := ScoreGoMutation([]byte(goReport), goProfile, "diff", 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := [7]int{s.Killed, s.Lived, s.NotCovered, s.TimedOut, s.CoveredUnrun, s.Inert, s.Generated}
	if want := [7]int{2, 1, 1, 1, 1, 2, 8}; got != want {
		t.Errorf("counts %v, want killed lived not-covered timed-out covered-unrun inert generated %v", got, want)
	}
	wantMissed := []string{
		"ops/ops.go:12:9  LIVED         ARITHMETIC_BASE",
		"ops/ops.go:20:7  NOT COVERED   CONDITIONALS_BOUNDARY",
	}
	if !reflect.DeepEqual(s.Missed, wantMissed) {
		t.Errorf("missed %q, want %q", s.Missed, wantMissed)
	}
	if s.TimedOutPct != 12.5 {
		t.Errorf("timed out %v%%, want 12.5", s.TimedOutPct)
	}
	// 2.5s × 1000 × 2 workers over the 4 that ran (2 killed, 1 lived, 1 timed out).
	if s.MsPerMutant != 1250 {
		t.Errorf("ms per mutant %v, want 1250", s.MsPerMutant)
	}
	for _, want := range []string{
		"### Mutation gate — go (diff)",
		"| 2 | 1 | 1 | 0 | 1 (12.5%) | 1 | 2 | 50% of 4 viable |",
		"_1250ms of wall clock per mutant across 2 worker(s)._",
		"**1 mutant(s) sit on code the coverage profile says RUNS,**",
		"ops/ops.go:30:7  COVERED-UNRUN CONDITIONALS_NEGATION",
		"**1 mutant(s) TIMED OUT — neither killed nor survived.**",
		"**Survivors**",
	} {
		if !strings.Contains(s.Summary, want) {
			t.Errorf("summary lacks %q:\n%s", want, s.Summary)
		}
	}
	if strings.Contains(s.Summary, "measured NONE") {
		t.Errorf("a run with viable mutants said it measured none:\n%s", s.Summary)
	}
}

// A profile that does not name the module still matches by file, and a block
// on the mutant's line that starts at or left of it is not the switch misread.
func TestScoreGoMutationReadsTheProfileExactly(t *testing.T) {
	report := `{"go_module":"","files":[{"file_name":"a.go","mutations":[
{"type":"T","status":"NOT COVERED","line":5,"column":4},
{"type":"T","status":"NOT COVERED","line":6,"column":4},
{"type":"T","status":"NOT COVERED","line":7,"column":4}]}]}`
	profile := "mode: set\na.go:5.9,6.2 1 1\na.go:6.4,7.2 1 1\na.go:7.1,8.2 1 1\n"
	s, err := ScoreGoMutation([]byte(report), profile, "diff", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.CoveredUnrun != 1 || s.NotCovered != 2 {
		t.Errorf("covered-unrun %d not-covered %d, want 1 and 2 (only a block starting RIGHT of the mutant)", s.CoveredUnrun, s.NotCovered)
	}
	if s.MsPerMutant != -1 {
		t.Errorf("no elapsed time, and a cost was reported: %v", s.MsPerMutant)
	}
	if strings.Contains(s.Summary, "wall clock") {
		t.Errorf("no elapsed time, and the summary spoke of wall clock:\n%s", s.Summary)
	}
	// With no profile at all nothing is corrected.
	s, _ = ScoreGoMutation([]byte(report), "", "diff", 1, nil)
	if s.CoveredUnrun != 0 || s.NotCovered != 3 {
		t.Errorf("no profile corrected a mutant: %+v", s)
	}
}

func TestScoreGoMutationSaysWhenItMeasuredNothing(t *testing.T) {
	report := `{"elapsed_time":1,"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"NOT VIABLE","line":1,"column":1}]}]}`
	s, err := ScoreGoMutation([]byte(report), "", "diff", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Summary, "**This run generated 1 mutant(s) and measured NONE of them.**") {
		t.Errorf("an unmeasured run did not say so:\n%s", s.Summary)
	}
	if !strings.Contains(s.Summary, "| 0 | 0 | 0 | 0 (0%) | 0 | 1 | 0% of 0 viable |") {
		t.Errorf("the empty table is wrong:\n%s", s.Summary)
	}
	if strings.Contains(s.Summary, "Survivors") || strings.Contains(s.Summary, "TIMED OUT") || strings.Contains(s.Summary, "sit on code") {
		t.Errorf("an empty run grew sections:\n%s", s.Summary)
	}
	if s.MsPerMutant != -1 {
		t.Errorf("nothing ran, and a cost was reported: %v", s.MsPerMutant)
	}
	if _, err := ScoreGoMutation([]byte("{"), "", "diff", 1, nil); err == nil || !strings.Contains(err.Error(), "not gremlins JSON") {
		t.Errorf("a report that does not parse: %v", err)
	}
}

// A report with no mutations measured nothing and says only that: no NaN
// percentage, no "measured NONE of 0", and a run gremlins timed at zero is still
// a cost worth printing.
func TestScoreGoMutationOfAnEmptyReportAndAZeroClock(t *testing.T) {
	s, err := ScoreGoMutation([]byte(`{"elapsed_time":1,"files":[]}`), "", "diff", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Generated != 0 || s.TimedOutPct != 0 {
		t.Errorf("an empty report: generated %d, timed out %v%%, want 0 and 0", s.Generated, s.TimedOutPct)
	}
	if strings.Contains(s.Summary, "measured NONE") || strings.Contains(s.Summary, "NaN") {
		t.Errorf("an empty report said too much:\n%s", s.Summary)
	}

	zero := `{"elapsed_time":0,"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1}]}]}`
	s, err = ScoreGoMutation([]byte(zero), "", "diff", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.MsPerMutant != 0 || !strings.Contains(s.Summary, "_0ms of wall clock per mutant across 1 worker(s)._") {
		t.Errorf("a zero clock is a cost of zero, printed: %v\n%s", s.MsPerMutant, s.Summary)
	}
	if _, reason := GoMutationVerdict(GoMutationRun{Report: []byte(zero), Canary: CanaryOK}); !strings.Contains(reason, "0% timed out, 0.0ms of wall clock per mutant") {
		t.Errorf("the measured line dropped a zero cost: %q", reason)
	}
}

// Rounding is half to even, as the scorer this replaces rounded.
func TestPythonRoundIsHalfToEven(t *testing.T) {
	for _, c := range []struct {
		x        float64
		decimals int
		want     float64
	}{{2.5, 0, 2}, {3.5, 0, 4}, {66.66666, 0, 67}, {12.25, 1, 12.2}, {12.35, 1, 12.4}, {0, 1, 0}} {
		if got := pythonRound(c.x, c.decimals); got != c.want {
			t.Errorf("pythonRound(%v, %d) = %v, want %v", c.x, c.decimals, got, c.want)
		}
	}
}

func TestGoMutationCanaryReadsTheControl(t *testing.T) {
	for out, want := range map[string]string{
		"Mutation testing completed\nKilled: 0, Lived: 1, Not covered: 0\n": CanaryOK,
		"Killed: 1, Lived: 0, Not covered: 0":                               CanaryBroken,
		"go: cannot find main module":                                       CanaryUnknown,
		"":                                                                  CanaryUnknown,
	} {
		if got := GoMutationCanary(out); got != want {
			t.Errorf("%q: %s, want %s", out, got, want)
		}
	}
}

func TestGoMutationVerdictSettlesEveryRun(t *testing.T) {
	clean := `{"elapsed_time":1,"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1}]}]}`
	timedOut := `{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1},{"type":"T","status":"TIMED OUT","line":2,"column":1}]}]}`
	atBudget := `{"files":[{"file_name":"a.go","mutations":[` + strings.Repeat(`{"type":"T","status":"KILLED","line":1,"column":1},`, 9) + `{"type":"T","status":"TIMED OUT","line":2,"column":1}]}]}`
	cases := map[string]struct {
		run    GoMutationRun
		state  int
		reason string
	}{
		"no report, gremlins clean":   {GoMutationRun{Status: 0}, 0, "gremlins had no results to report"},
		"no report, gremlins broken":  {GoMutationRun{Status: 3}, 2, "gremlins exited 3 and wrote no mutation-go.json"},
		"a report that does not read": {GoMutationRun{Report: []byte("{")}, 2, "could not be read"},
		"gremlins broke with a report": {GoMutationRun{Status: 10, Report: []byte(clean), Canary: CanaryOK}, 2,
			"gremlins exited 10 — a broken run"},
		"too many timed out":    {GoMutationRun{Report: []byte(timedOut), Canary: CanaryOK}, 2, "50% of mutants TIMED OUT, over the 10% budget"},
		"exactly at the budget": {GoMutationRun{Report: []byte(atBudget), Canary: CanaryOK}, 0, "every viable mutant was caught"},
		"a broken canary":       {GoMutationRun{Report: []byte(clean), Canary: CanaryBroken}, 2, "the harness scores unrun tests as kills"},
		"an unread canary":      {GoMutationRun{Report: []byte(clean), Canary: CanaryUnknown}, 0, "every viable mutant was caught"},
		"all caught":            {GoMutationRun{Report: []byte(clean), Canary: CanaryOK, Workers: 4}, 0, "every viable mutant was caught\nmeasured: 0 missed, 0% timed out, 4000.0ms of wall clock per mutant"},
		"survivors":             {GoMutationRun{Report: []byte(goReport), Profile: goProfile, Canary: CanaryOK}, 2, "12.5% of mutants TIMED OUT"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			state, reason := GoMutationVerdict(c.run)
			if state != c.state || !strings.Contains(reason, c.reason) {
				t.Fatalf("settled %d %q, want %d naming %q", state, reason, c.state, c.reason)
			}
		})
	}
	survivors := `{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1},{"type":"T","status":"LIVED","line":2,"column":3}]}]}`
	// A survivor is a 1 only once the classifier has answered — with nothing,
	// here — and said it is not noise; see TestGoMutationVerdictReadsTheClassification.
	state, reason := GoMutationVerdict(GoMutationRun{Report: []byte(survivors), Canary: CanaryOK, Classified: []byte(`{"noise":[]}`)})
	if state != 1 || !strings.Contains(reason, "1 mutant(s) survived or were never covered") || !strings.Contains(reason, "a.go:2:3  LIVED") {
		t.Errorf("survivors: %d %q", state, reason)
	}
	if strings.Contains(reason, "wall clock") {
		t.Errorf("no elapsed time, and the measured line spoke of wall clock: %q", reason)
	}
}

// The classification the testkit's gate answered is folded in by coordinate:
// a named survivor leaves Missed, leaves the kill rate's denominator, and is
// LISTED with its reason — a forgiveness nobody can read is a suppression.
func TestScoreGoMutationForgivesWhatTheClassifierNamedAndSaysSo(t *testing.T) {
	report := `{"elapsed_time":1,"files":[{"file_name":"a.go","mutations":[
		{"type":"T","status":"KILLED","line":1,"column":1},
		{"type":"ARITHMETIC_BASE","status":"NOT COVERED","line":3,"column":16},
		{"type":"T","status":"LIVED","line":5,"column":2}]}]}`
	noise, err := ParseGoMutationNoise([]byte(`{"killed":1,"noise":[{"file":"a.go","line":3,"column":16,"type":"ARITHMETIC_BASE","status":"NOT COVERED","noise_reason":"declaration"}],"real":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	s, err := ScoreGoMutation([]byte(report), "", "diff", 1, noise)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Forgiven) != 1 || s.NotCovered != 0 || s.Lived != 1 || len(s.Missed) != 1 {
		t.Errorf("forgiven %d not-covered %d lived %d missed %d, want 1 0 1 1: %+v", len(s.Forgiven), s.NotCovered, s.Lived, len(s.Missed), s)
	}
	for _, want := range []string{
		"| 1 | 1 | 0 | 1 | 0 (0%) | 0 | 0 | 50% of 2 viable |",
		"1 mutant(s) forgiven — unkillable by construction, not untested.",
		"a.go:3:16  NOT COVERED   ARITHMETIC_BASE  [declaration]",
		"a.go:5:2  LIVED         T",
	} {
		if !strings.Contains(s.Summary, want) {
			t.Errorf("the summary lacks %q:\n%s", want, s.Summary)
		}
	}
	// And a run that forgave nothing says nothing about forgiveness: the
	// block is evidence of what was set aside, not a heading for an empty
	// list.
	if plain, _ := ScoreGoMutation([]byte(report), "", "diff", 1, GoMutationNoise{}); strings.Contains(plain.Summary, "forgiven —") || !strings.Contains(plain.Summary, "| 1 | 1 | 1 | 0 | 0 (0%) | 0 | 0 | 33% of 3 viable |") {
		t.Errorf("a run with nothing forgiven printed the forgiven block, or miscounted:\n%s", plain.Summary)
	}
	// A KILLED mutant at a named coordinate is a kill: the classifier only
	// speaks about survivors, and a name it gave a mutant that was later
	// killed by a re-run must not turn the kill into a forgiveness.
	noise["a.go:1:1"] = "declaration"
	s, _ = ScoreGoMutation([]byte(report), "", "diff", 1, noise)
	if s.Killed != 1 || len(s.Forgiven) != 1 {
		t.Errorf("a kill was reclassified: killed %d forgiven %d", s.Killed, len(s.Forgiven))
	}
}

// ParseGoMutationNoise reads the testkit's wire shape and nothing else.
func TestParseGoMutationNoiseReadsTheTestkitsShape(t *testing.T) {
	noise, err := ParseGoMutationNoise([]byte(`{"noise":[{"file":"p/q.go","line":7,"column":9,"noise_reason":"switch-case"},{"file":"p/q.go","line":8,"column":1,"noise_reason":"declaration"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if noise["p/q.go:7:9"] != "switch-case" || noise["p/q.go:8:1"] != "declaration" || len(noise) != 2 {
		t.Errorf("noise = %v", noise)
	}
	if _, err := ParseGoMutationNoise([]byte("| killed |")); err == nil || !strings.Contains(err.Error(), "not mutation-gate JSON") {
		t.Errorf("a markdown table parsed as a classification: %v", err)
	}
	if noise, err := ParseGoMutationNoise([]byte(`{"noise":[]}`)); err != nil || noise == nil || len(noise) != 0 {
		t.Errorf("an empty classification is an answer, not an absence: %v %v", noise, err)
	}
}

// The verdict with and without a classification: same report, three answers.
func TestGoMutationVerdictReadsTheClassification(t *testing.T) {
	report := `{"files":[{"file_name":"a.go","mutations":[{"type":"ARITHMETIC_BASE","status":"NOT COVERED","line":3,"column":16}]}]}`
	named := `{"noise":[{"file":"a.go","line":3,"column":16,"noise_reason":"declaration"}]}`
	cases := map[string]struct {
		classified, classifyErr string
		state                   int
		reason                  string
	}{
		"named":                      {named, "", 0, "every viable mutant was caught"},
		"answered, not named":        {`{"noise":[]}`, "", 1, "1 mutant(s) survived or were never covered"},
		"no answer, a reason":        {"", "mutation-gate: could not read", 2, "did not answer (mutation-gate: could not read)"},
		"no answer, no reason":       {"", "", 2, "did not answer (mutation-gate wrote nothing)"},
		"an answer that is not JSON": {"| killed |", "", 2, "not mutation-gate JSON"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			state, reason := GoMutationVerdict(GoMutationRun{
				Report: []byte(report), Canary: CanaryOK, Workers: 1,
				Classified: []byte(c.classified), ClassifyErr: c.classifyErr,
			})
			if state != c.state || !strings.Contains(reason, c.reason) {
				t.Errorf("state %d, want %d; reason lacks %q:\n%s", state, c.state, c.reason, reason)
			}
		})
	}
	// A clean run needs no classification: nothing survived to classify.
	state, _ := GoMutationVerdict(GoMutationRun{
		Report: []byte(`{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1}]}]}`),
		Canary: CanaryOK, Workers: 1,
	})
	if state != 0 {
		t.Errorf("a clean run with no classification settled %d", state)
	}
}
