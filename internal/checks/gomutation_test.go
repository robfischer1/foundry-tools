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
	s, err := ScoreGoMutation([]byte(goReport), goProfile, "diff", 2, nil, nil)
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
		"| 2 | 1 | 1 | 0 | 1 (12.5%) | 1 | 0 | 2 | 50% of 4 viable |",
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
	s, err := ScoreGoMutation([]byte(report), profile, "diff", 0, nil, nil)
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
	s, _ = ScoreGoMutation([]byte(report), "", "diff", 1, nil, nil)
	if s.CoveredUnrun != 0 || s.NotCovered != 3 {
		t.Errorf("no profile corrected a mutant: %+v", s)
	}
}

func TestScoreGoMutationSaysWhenItMeasuredNothing(t *testing.T) {
	report := `{"elapsed_time":1,"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"NOT VIABLE","line":1,"column":1}]}]}`
	s, err := ScoreGoMutation([]byte(report), "", "diff", 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Summary, "**This run generated 1 mutant(s) and measured NONE of them.**") {
		t.Errorf("an unmeasured run did not say so:\n%s", s.Summary)
	}
	if !strings.Contains(s.Summary, "| 0 | 0 | 0 | 0 | 0 (0%) | 0 | 0 | 1 | 0% of 0 viable |") {
		t.Errorf("the empty table is wrong:\n%s", s.Summary)
	}
	if strings.Contains(s.Summary, "Survivors") || strings.Contains(s.Summary, "TIMED OUT") || strings.Contains(s.Summary, "sit on code") {
		t.Errorf("an empty run grew sections:\n%s", s.Summary)
	}
	if s.MsPerMutant != -1 {
		t.Errorf("nothing ran, and a cost was reported: %v", s.MsPerMutant)
	}
	if _, err := ScoreGoMutation([]byte("{"), "", "diff", 1, nil, nil); err == nil || !strings.Contains(err.Error(), "not gremlins JSON") {
		t.Errorf("a report that does not parse: %v", err)
	}
}

// A report with no mutations measured nothing and says only that: no NaN
// percentage, no "measured NONE of 0", and a run gremlins timed at zero is still
// a cost worth printing.
func TestScoreGoMutationOfAnEmptyReportAndAZeroClock(t *testing.T) {
	s, err := ScoreGoMutation([]byte(`{"elapsed_time":1,"files":[]}`), "", "diff", 1, nil, nil)
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
	s, err = ScoreGoMutation([]byte(zero), "", "diff", 1, nil, nil)
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
	s, err := ScoreGoMutation([]byte(report), "", "diff", 1, noise, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Forgiven) != 1 || s.NotCovered != 0 || s.Lived != 1 || len(s.Missed) != 1 {
		t.Errorf("forgiven %d not-covered %d lived %d missed %d, want 1 0 1 1: %+v", len(s.Forgiven), s.NotCovered, s.Lived, len(s.Missed), s)
	}
	for _, want := range []string{
		"| 1 | 1 | 0 | 1 | 0 (0%) | 0 | 0 | 0 | 50% of 2 viable |",
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
	if plain, _ := ScoreGoMutation([]byte(report), "", "diff", 1, GoMutationNoise{}, nil); strings.Contains(plain.Summary, "forgiven —") || !strings.Contains(plain.Summary, "| 1 | 1 | 1 | 0 | 0 (0%) | 0 | 0 | 0 | 33% of 3 viable |") {
		t.Errorf("a run with nothing forgiven printed the forgiven block, or miscounted:\n%s", plain.Summary)
	}
	// A KILLED mutant at a named coordinate is a kill: the classifier only
	// speaks about survivors, and a name it gave a mutant that was later
	// killed by a re-run must not turn the kill into a forgiveness.
	noise["a.go:1:1"] = "declaration"
	s, _ = ScoreGoMutation([]byte(report), "", "diff", 1, noise, nil)
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

// A MUTANT IN A `package main` IS NOT GRADED, and the bucket exists for the
// direction that reads GREEN. This gremlins resolves the file's package from its
// package clause, runs the module root's tests for it, and reports a verdict
// about other code — KILLED where the root has no package, LIVED where it has
// one. The classifier cannot reach the KILLED case at all (it only speaks about
// survivors), so without this bucket a lane over cmd/ publishes kills it never
// earned.
func TestScoreGoMutationDoesNotCountAMutantItGradedAgainstTheWrongPackage(t *testing.T) {
	report := `{"elapsed_time":1,"files":[
{"file_name":"cmd/tool/main.go","mutations":[
{"type":"ARITHMETIC_BASE","status":"KILLED","line":4,"column":35},
{"type":"CONDITIONALS_NEGATION","status":"LIVED","line":6,"column":5},
{"type":"INVERT_NEGATIVES","status":"NOT VIABLE","line":8,"column":3}]},
{"file_name":"lib/lib.go","mutations":[
{"type":"T","status":"KILLED","line":1,"column":1}]}]}`
	s, err := ScoreGoMutation([]byte(report), "", "diff", 1, nil, GoMainFiles{"cmd/tool/main.go": true})
	if err != nil {
		t.Fatal(err)
	}
	if s.Killed != 1 || s.Lived != 0 || len(s.Ungraded) != 2 || s.Viable() != 1 || len(s.Missed) != 0 {
		t.Errorf("killed %d lived %d ungraded %d viable %d missed %d, want 1 0 2 1 0 — only the library's kill was graded",
			s.Killed, s.Lived, len(s.Ungraded), s.Viable(), len(s.Missed))
	}
	// NOT VIABLE stays inert. gremlins decided it without running anything, so a
	// runner that grades the wrong package does not make it wrong — and calling
	// it ungraded would inflate the hole with mutants that were never a question.
	if s.Inert != 1 || s.Generated != 4 {
		t.Errorf("inert %d of %d generated, want 1 of 4", s.Inert, s.Generated)
	}
	for _, want := range []string{
		"| 1 | 0 | 0 | 0 | 0 (0%) | 0 | 2 | 1 | 100% of 1 viable |",
		"**2 mutant(s) were NOT GRADED — they sit in a `package main`.**",
		"gremlins#268, fix open at #306",
		"cmd/tool/main.go:4:35  KILLED        ARITHMETIC_BASE",
		"cmd/tool/main.go:6:5  LIVED         CONDITIONALS_NEGATION",
	} {
		if !strings.Contains(s.Summary, want) {
			t.Errorf("the summary lacks %q:\n%s", want, s.Summary)
		}
	}
	// A FORGIVENESS NOBODY CAN READ IS A SUPPRESSION, and so is an exclusion:
	// the inert mutant is not in the list, because it was not excluded.
	if strings.Contains(s.Summary, "INVERT_NEGATIVES") {
		t.Errorf("an inert mutant was listed as ungraded:\n%s", s.Summary)
	}

	// AND THE CONTROL IS WHAT SWITCHES THIS ON: distrusting nothing, the same
	// report scores the false kill as a kill and the false survivor as a gap.
	// This is the behaviour a fixed gremlins restores, and the reason the
	// exclusion is keyed on a measurement rather than a constant.
	trusted, err := ScoreGoMutation([]byte(report), "", "diff", 1, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if trusted.Killed != 2 || trusted.Lived != 1 || len(trusted.Ungraded) != 0 || len(trusted.Missed) != 1 {
		t.Errorf("distrusting nothing: killed %d lived %d ungraded %d missed %d, want 2 1 0 1",
			trusted.Killed, trusted.Lived, len(trusted.Ungraded), len(trusted.Missed))
	}
	if strings.Contains(trusted.Summary, "NOT GRADED") {
		t.Errorf("a run that distrusted nothing spoke of ungraded mutants:\n%s", trusted.Summary)
	}
}

// UNGRADED OUTRANKS FORGIVEN, because they are opposite claims about the same
// mutant and it can only be in one column. The testkit's classifier forgives a
// survivor in a `package main` too (its own package-main class), but forgiveness
// says "no test could ever kill this" — and here the truth is that nobody
// looked. The scorer asks the harder question first.
func TestScoreGoMutationPrefersUngradedToForgiven(t *testing.T) {
	report := `{"files":[{"file_name":"cmd/tool/main.go","mutations":[{"type":"T","status":"LIVED","line":6,"column":5}]}]}`
	noise := GoMutationNoise{"cmd/tool/main.go:6:5": "package-main"}
	s, err := ScoreGoMutation([]byte(report), "", "diff", 1, noise, GoMainFiles{"cmd/tool/main.go": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Ungraded) != 1 || len(s.Forgiven) != 0 {
		t.Errorf("ungraded %d forgiven %d, want 1 and 0 — a mutant nobody graded is not a mutant nobody could kill",
			len(s.Ungraded), len(s.Forgiven))
	}
	if strings.Contains(s.Summary, "unkillable by construction") {
		t.Errorf("the summary called an ungraded mutant unkillable:\n%s", s.Summary)
	}
	// A run that graded NOTHING says so, and does not blame the diff for it.
	if !strings.Contains(s.Summary, "measured NONE of them") || !strings.Contains(s.Summary, "Every mutant it could have graded sits") {
		t.Errorf("a wholly ungraded run did not say why:\n%s", s.Summary)
	}
	if strings.Contains(s.Summary, "no CHANGED LINE carried a") {
		t.Errorf("the diff was blamed for the runner's bug:\n%s", s.Summary)
	}
}

// The package-main control decides whether a main package's mutants count, so
// ONE report settles differently depending on it — and a broken control is not
// fatal the way a broken harness control is: it is the expected answer today.
func TestGoMutationVerdictTrustsAMainPackageOnlyWhenItsControlSaysSo(t *testing.T) {
	// One mutant in a main package, KILLED: the false green, and the shape 36 of
	// the fleet's 38 Go repos produce.
	kill := `{"files":[{"file_name":"cmd/tool/main.go","mutations":[{"type":"ARITHMETIC_BASE","status":"KILLED","line":4,"column":35}]}]}`
	// The same, LIVED: the false red, which a module with a root package gets.
	live := `{"files":[{"file_name":"cmd/tool/main.go","mutations":[{"type":"T","status":"LIVED","line":6,"column":5}]}]}`
	// A real gap beside an ungraded mutant: the gap still decides.
	both := `{"files":[{"file_name":"cmd/tool/main.go","mutations":[{"type":"T","status":"KILLED","line":4,"column":35}]},
{"file_name":"lib/lib.go","mutations":[{"type":"T","status":"LIVED","line":9,"column":2}]}]}`
	files := GoMainFiles{"cmd/tool/main.go": true}
	cases := map[string]struct {
		report  string
		canary  string
		files   GoMainFiles
		listErr string
		state   int
		reason  string
	}{
		"broken, so a kill in a main package is not a kill": {kill, CanaryBroken, files, "", 0, "NOTHING WAS GRADED"},
		"unread, which is not a clearance":                  {kill, CanaryUnknown, files, "", 0, "NOTHING WAS GRADED"},
		"unread, and a survivor is not graded either":       {live, CanaryUnknown, files, "", 0, "NOTHING WAS GRADED"},
		"fixed, and the kill is a kill":                     {kill, CanaryOK, files, "", 0, "every viable mutant was caught"},
		"fixed, and the survivor is a survivor":             {live, CanaryOK, files, "", 1, "1 mutant(s) survived or were never covered"},
		"a real gap outranks the hole":                      {both, CanaryBroken, files, "", 1, "1 mutant(s) survived or were never covered"},
		// NOTHING TO EXCLUDE WITH is a could-not-measure: the runner grades a
		// main package wrong and the lane cannot say which files are in one, so
		// every count may be about other code and the gate cannot point at which.
		"broken, and the files could not be listed": {kill, CanaryBroken, nil, "go list exited 1", 2, "could not list which files are in one"},
		// …and moot once the runner is fixed: there is nothing to exclude.
		"fixed, so a failed listing is moot": {kill, CanaryOK, nil, "go list exited 1", 0, "every viable mutant was caught"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			state, reason := GoMutationVerdict(GoMutationRun{
				Report: []byte(c.report), Canary: CanaryOK, Workers: 1,
				Classified: []byte(`{"noise":[]}`),
				MainCanary: c.canary, MainFiles: c.files, MainFilesErr: c.listErr,
			})
			if state != c.state || !strings.Contains(reason, c.reason) {
				t.Errorf("settled %d, want %d; reason lacks %q:\n%s", state, c.state, c.reason, reason)
			}
		})
	}
	// A GREEN THAT SAYS IT VERIFIED NOTHING. Exit 0, because there is no test
	// gap to point at and no committer who can fix #268 — but the reason says so
	// in as many words, rather than claiming every viable mutant was caught.
	_, reason := GoMutationVerdict(GoMutationRun{
		Report: []byte(kill), Canary: CanaryOK, Workers: 1,
		Classified: []byte(`{"noise":[]}`), MainCanary: CanaryBroken, MainFiles: files,
	})
	if strings.Contains(reason, "every viable mutant was caught") {
		t.Errorf("a run that graded nothing claimed every mutant was caught:\n%s", reason)
	}
	if !strings.Contains(reason, "This run did not verify the tests") || !strings.Contains(reason, "cmd/tool/main.go:4:35") {
		t.Errorf("the reason must say what it did not do, and name what it skipped:\n%s", reason)
	}
}
