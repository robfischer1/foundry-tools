package checks

import (
	"encoding/json"
	"path"
	"reflect"
	"strings"
	"testing"
)

// THE CONTRACT, ON FIXTURES: a run that reuses some units' stored gradings
// answers the same state and the same findings as a run that grades every
// unit cold. Each fixture is one gomutants report over two or more units; the
// cold run scores it whole, and the incremental run scores only the fresh
// units' share of it while the rest come back as gradings — built, stored and
// read exactly as the lane would (BuildGradings, through JSON, as the lookup
// answers them).

type foldFixture struct {
	name     string
	state    int
	dir      string
	files    map[string]string // module-relative file -> its report entry's mutations JSON
	profile  string
	noise    string
	misgrade GoMisgradedFiles
	reuse    []string // module-relative files whose units come back reused
}

func reportOf(files map[string]string, only func(string) bool) []byte {
	var parts []string
	for f, muts := range files {
		if only(f) {
			parts = append(parts, `{"file_name":"`+f+`","mutations":[`+muts+`]}`)
		}
	}
	return []byte(`{"elapsed_time":2,"files":[` + strings.Join(parts, ",") + `]}`)
}

func runFor(fx foldFixture, report []byte) GoMutationRun {
	return GoMutationRun{Report: report, Profile: fx.profile, Canary: CanaryOK, MainCanary: CanaryBroken,
		MisgradedFiles: fx.misgrade, Workers: 4, Classified: []byte(fx.noise)}
}

// storedGradingsOf grades the reused files alone, as an earlier trusted run
// would have, and answers them as the lookup would.
func storedGradingsOf(t *testing.T, fx foldFixture) []ReusedGrading {
	t.Helper()
	reused := map[string]bool{}
	for _, f := range fx.reuse {
		reused[f] = true
	}
	earlier := GoMutationVerdictReusing(runFor(fx, reportOf(fx.files, func(f string) bool { return reused[f] })), fx.dir, nil)
	if earlier.State == 2 {
		t.Fatalf("%s: the earlier run did not measure: %s", fx.name, earlier.Reason)
	}
	owner := func(f string) (string, bool) { return path.Dir(f), true }
	var keys []UnitKey
	for f := range reused {
		keys = append(keys, UnitKey{Unit: path.Dir(path.Join(fx.dir, f)), Hash: "h", Ranges: "r"})
	}
	gradings := GoGradings(earlier.Fresh, fx.dir, "E", GoGradingTrusted(earlier.State, CanaryOK), keys, owner)
	raw, _ := json.Marshal(gradings)
	var out []ReusedGrading
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		if !gradings[i].Reusable {
			t.Fatalf("%s: a fixture's reused unit must be reusable: %+v", fx.name, gradings[i])
		}
		out[i].RunName, out[i].Lane, out[i].RunNumber, out[i].Sha = "forge/mutation-x-1", "mutation", 41, "abcdef0123456789"
	}
	return out
}

func TestAReuseRunAnswersWhatAColdRunAnswers(t *testing.T) {
	for _, fx := range []foldFixture{
		{name: "every mutant killed", state: 0, dir: ".",
			files: map[string]string{
				"a/x.go": `{"type":"T","status":"KILLED","line":1,"column":1},{"type":"V","status":"NOT VIABLE","line":2,"column":1}`,
				"b/y.go": `{"type":"T","status":"KILLED","line":3,"column":1}`,
			},
			noise: `{"noise":[]}`, reuse: []string{"a/x.go"}},
		{name: "survivors in the reused unit and the fresh one", state: 1, dir: ".",
			files: map[string]string{
				"a/x.go": `{"type":"T","status":"LIVED","line":4,"column":2},{"type":"U","status":"KILLED","line":4,"column":2},{"type":"N","status":"NOT COVERED","line":9,"column":1},{"type":"V","status":"SKIPPED","line":10,"column":1}`,
				"b/y.go": `{"type":"T","status":"LIVED","line":1,"column":1}`,
				"c/z.go": `{"type":"T","status":"KILLED","line":1,"column":1},{"type":"V","status":"NOT VIABLE","line":2,"column":1}`,
			},
			noise: `{"noise":[]}`, reuse: []string{"a/x.go", "c/z.go"}},
		{name: "forgiven, covered-unrun, ungraded and a fresh timeout, in a nested module", state: 0, dir: "tools/forge",
			files: map[string]string{
				"a/x.go":       `{"type":"F","status":"NOT COVERED","line":5,"column":16},{"type":"S","status":"NOT COVERED","line":7,"column":2}`,
				"cmd/m/m.go":   `{"type":"T","status":"KILLED","line":1,"column":1}`,
				"b/y.go":       `{"type":"O","status":"TIMED OUT","line":2,"column":1},{"type":"K","status":"KILLED","line":3,"column":1},{"type":"L","status":"LIVED","line":4,"column":1}`,
				"b/y_extra.go": `{"type":"K","status":"KILLED","line":1,"column":1},{"type":"K","status":"KILLED","line":2,"column":1},{"type":"K","status":"KILLED","line":3,"column":1},{"type":"K","status":"KILLED","line":4,"column":1},{"type":"K","status":"KILLED","line":5,"column":1},{"type":"K","status":"KILLED","line":6,"column":1},{"type":"K","status":"KILLED","line":7,"column":1},{"type":"K","status":"KILLED","line":8,"column":1},{"type":"K","status":"KILLED","line":9,"column":1},{"type":"K","status":"KILLED","line":10,"column":1},{"type":"K","status":"KILLED","line":11,"column":1}`,
			},
			profile:  "mode: set\nm/a/x.go:7.10,8.2 1 1\n",
			noise:    `{"noise":[{"file":"a/x.go","line":5,"column":16,"noise_reason":"declaration"},{"file":"b/y.go","line":4,"column":1,"noise_reason":"x"}]}`,
			misgrade: GoMisgradedFiles{"cmd/m/m.go": true},
			reuse:    []string{"a/x.go", "cmd/m/m.go"}},
	} {
		t.Run(fx.name, func(t *testing.T) {
			cold := GoMutationVerdictReusing(runFor(fx, reportOf(fx.files, func(string) bool { return true })), fx.dir, nil)
			reused := storedGradingsOf(t, fx)
			isReused := map[string]bool{}
			for _, f := range fx.reuse {
				isReused[f] = true
			}
			fresh := reportOf(fx.files, func(f string) bool { return !isReused[f] })
			warm := GoMutationVerdictReusing(runFor(fx, fresh), fx.dir, reused)
			if cold.State != fx.state || (fx.name != "every mutant killed" && len(cold.Score.Findings) == 0) {
				t.Fatalf("the fixture is not what it claims: state %d, %d findings\n%s", cold.State, len(cold.Score.Findings), cold.Reason)
			}
			if warm.State != cold.State {
				t.Fatalf("state: reuse %d, cold %d\nreuse: %s\ncold: %s", warm.State, cold.State, warm.Reason, cold.Reason)
			}
			// The verdict's findings, and every finding the score made — a pass
			// carries none of them, and they must still agree.
			if !reflect.DeepEqual(warm.Findings, cold.Findings) || !reflect.DeepEqual(warm.Score.Findings, cold.Score.Findings) {
				t.Fatalf("findings differ:\nreuse %+v\ncold  %+v\nscore reuse %+v\nscore cold  %+v", warm.Findings, cold.Findings, warm.Score.Findings, cold.Score.Findings)
			}
			wc, cc := *warm.Score, *cold.Score
			if wc.Killed != cc.Killed || wc.Lived != cc.Lived || wc.NotCovered != cc.NotCovered || wc.Inert != cc.Inert || wc.Generated != cc.Generated ||
				wc.CoveredUnrun != cc.CoveredUnrun || len(wc.Forgiven) != len(cc.Forgiven) || len(wc.Ungraded) != len(cc.Ungraded) ||
				wc.TimedOut != cc.TimedOut || wc.ProfileDisagrees != cc.ProfileDisagrees || wc.TimedOutPct != cc.TimedOutPct {
				t.Fatalf("counts differ:\nreuse %+v\ncold  %+v", wc, cc)
			}
			if first := func(r string) string { return strings.SplitN(r, "\n", 2)[0] }; first(warm.Reason) != first(cold.Reason) {
				t.Fatalf("the verdict lines differ: %q / %q", first(warm.Reason), first(cold.Reason))
			}
			if !strings.Contains(warm.Reason, "### Reused — ") || strings.Contains(cold.Reason, "### Reused") {
				t.Fatal("only a reuse run says what it reused")
			}
			if len(warm.Fresh) >= len(cold.Fresh) {
				t.Fatal("a reuse run grades less than a cold one")
			}
		})
	}
}

// Every unit reused: the run graded nothing itself (no report, exit 0) and
// still answers the cold verdict — survivors included, never "the classifier
// did not answer".
func TestARunThatReusedEveryUnitAnswersTheColdVerdict(t *testing.T) {
	fx := foldFixture{name: "all", dir: ".", noise: `{"noise":[]}`,
		files: map[string]string{"a/x.go": `{"type":"T","status":"LIVED","line":1,"column":1},{"type":"K","status":"KILLED","line":2,"column":1}`},
		reuse: []string{"a/x.go"}}
	cold := GoMutationVerdictReusing(runFor(fx, reportOf(fx.files, func(string) bool { return true })), ".", nil)
	warm := GoMutationVerdictReusing(GoMutationRun{Canary: CanaryOK, MainCanary: CanaryOK}, ".", storedGradingsOf(t, fx))
	if warm.State != 1 || cold.State != 1 || !reflect.DeepEqual(warm.Findings, cold.Findings) || warm.Fresh != nil {
		t.Fatalf("warm %d %+v\ncold %d %+v", warm.State, warm.Findings, cold.State, cold.Findings)
	}
	// A fresh run that broke is a broken run, whatever was reused.
	broken := GoMutationVerdictReusing(GoMutationRun{Status: 1, Canary: CanaryOK}, ".", storedGradingsOf(t, fx))
	if broken.State != 2 || !strings.Contains(broken.Reason, "gomutants exited 1 and wrote no mutation-go.json") {
		t.Fatalf("a broken fresh run: %d %s", broken.State, broken.Reason)
	}
	// A fresh report that does not read is could-not-run, whatever was reused.
	garbled := GoMutationVerdictReusing(GoMutationRun{Report: []byte("{"), Canary: CanaryOK}, ".", storedGradingsOf(t, fx))
	if garbled.State != 2 || !strings.Contains(garbled.Reason, "could not be read") {
		t.Fatalf("a garbled fresh report: %d %s", garbled.State, garbled.Reason)
	}
}

func TestTheReusedSectionNamesEachUnitAndItsRun(t *testing.T) {
	got := ReusedSection([]ReusedGrading{{Unit: "internal/a", Lane: "mutation", RunNumber: 41, Sha: "abcdef0123456789", GradedAt: "2026-10-03T16:00:00Z",
		Counts: GradingCounts{Generated: 9, Killed: 5, Lived: 1, NotCovered: 1}}})
	for _, want := range []string{"### Reused — 1 unit(s)", "| unit | graded by | at | killed | survived | other |\n|---|", "| internal/a | mutation run #41 | abcdef012345 2026-10-03T16:00:00Z | 5 | 2 | 2 |"} {
		if !strings.Contains(got, want) {
			t.Errorf("section lacks %q:\n%s", want, got)
		}
	}
}

// A score over no stated worker count counts one, as every run does.
func TestAScoreCountsAtLeastOneWorker(t *testing.T) {
	elapsed := 2.0
	s := scoreGoMutants([]ScoredMutant{{Outcome: OutcomeKilled}, {Outcome: OutcomeLived}}, GradingCounts{}, &elapsed, "diff", 0)
	if s.MsPerMutant != 1000 {
		t.Fatalf("ms per mutant = %v, want 2s × 1 worker / 2 mutants", s.MsPerMutant)
	}
}
