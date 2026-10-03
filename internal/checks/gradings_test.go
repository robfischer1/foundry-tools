package checks

import (
	"path"
	"reflect"
	"testing"
)

// dirOwner is a Go-shaped owner for the tests: a file's unit is its
// directory, and a file under nowhere/ has none.
func dirOwner(file string) (string, bool) {
	d := path.Dir(file)
	return d, d != "nowhere"
}

func TestBuildGradingsSplitsARunIntoItsUnits(t *testing.T) {
	keys := []UnitKey{
		{Unit: "b", Hash: "hb", Ranges: "rb", Closure: []string{"b"}},
		{Unit: "a", Hash: "ha", Ranges: "ra", Closure: []string{"a", "c"}},
		{Unit: "empty", Hash: "he", Ranges: "re"},
		{Unit: "broken", Err: "go list failed"},
	}
	mutants := []ScoredMutant{
		{File: "a/x.go", Line: 9, Col: 1, Op: "T", Outcome: OutcomeLived},
		{File: "a/x.go", Line: 2, Col: 5, Op: "T", Outcome: OutcomeKilled},
		{File: "a/x.go", Line: 2, Col: 3, Op: "U", Outcome: OutcomeNotCovered, Disputed: true},
		{File: "a/x.go", Line: 2, Col: 3, Op: "T", Outcome: OutcomeForgiven, Detail: "declaration"},
		{File: "a/w.go", Line: 2, Col: 3, Op: "T", Outcome: OutcomeCoveredUnrun},
		{File: "a/w.go", Line: 2, Col: 3, Op: "T", Outcome: OutcomeUngraded, Status: "KILLED"},
		{File: "a/w.go", Line: 2, Col: 3, Op: "T", Outcome: OutcomeInert},
		{File: "a/w.go", Line: 2, Col: 3, Op: "T", Outcome: "mystery"},
		{File: "b/y.go", Line: 1, Col: 1, Op: "T", Outcome: OutcomeTimedOut},
		{File: "broken/z.go", Line: 1, Col: 1, Op: "T", Outcome: OutcomeKilled},
		{File: "elsewhere/q.go", Line: 1, Col: 1, Op: "T", Outcome: OutcomeLived},
		{File: "elsewhere/q.go", Line: 2, Col: 1, Op: "T", Outcome: OutcomeKilled},
		{File: "nowhere/n.go", Line: 1, Col: 1, Op: "T", Outcome: OutcomeLived},
	}
	got := BuildGradings("go", "E", keys, dirOwner, mutants, true)
	units := []string{}
	for _, g := range got {
		units = append(units, g.Unit)
	}
	if want := []string{"b", "a", "empty", "broken", "elsewhere", "nowhere"}; !reflect.DeepEqual(units, want) {
		t.Fatalf("units = %v, want %v (keys in order, then the units no key names)", units, want)
	}
	a := got[1]
	if a.Lang != "go" || a.Engine != "E" || a.Hash != "ha" || a.Ranges != "ra" || !reflect.DeepEqual(a.Closure, []string{"a", "c"}) {
		t.Fatalf("a's key did not ride: %+v", a)
	}
	wantCounts := GradingCounts{Generated: 8, Killed: 1, Lived: 1, NotCovered: 1, CoveredUnrun: 1, Forgiven: 1, Ungraded: 1, Inert: 1, Disputed: 1}
	if a.Counts != wantCounts {
		t.Fatalf("a's counts = %+v, want %+v", a.Counts, wantCounts)
	}
	wantListed := []ScoredMutant{
		{File: "a/w.go", Line: 2, Col: 3, Op: "T", Outcome: OutcomeCoveredUnrun},
		{File: "a/w.go", Line: 2, Col: 3, Op: "T", Outcome: "mystery"},
		{File: "a/w.go", Line: 2, Col: 3, Op: "T", Outcome: OutcomeUngraded, Status: "KILLED"},
		{File: "a/x.go", Line: 2, Col: 3, Op: "T", Outcome: OutcomeForgiven, Detail: "declaration"},
		{File: "a/x.go", Line: 2, Col: 3, Op: "U", Outcome: OutcomeNotCovered, Disputed: true},
		{File: "a/x.go", Line: 9, Col: 1, Op: "T", Outcome: OutcomeLived},
	}
	if !reflect.DeepEqual(a.Mutants, wantListed) {
		t.Fatalf("a's mutants:\n%+v\nwant (killed and inert are counts; sorted by file, line, col, op, outcome)\n%+v", a.Mutants, wantListed)
	}
	for i, c := range []struct {
		reusable bool
		why      string
	}{
		{false, WhyNotTimeout}, {true, ""}, {true, ""}, {false, WhyNotUnkeyable}, {false, WhyNotUnkeyable}, {false, WhyNotUnkeyable},
	} {
		if got[i].Reusable != c.reusable || got[i].WhyNot != c.why {
			t.Errorf("%s: reusable %v why %q, want %v %q", got[i].Unit, got[i].Reusable, got[i].WhyNot, c.reusable, c.why)
		}
	}
	if got[2].Mutants == nil || len(got[2].Mutants) != 0 || got[2].Counts != (GradingCounts{}) {
		t.Fatalf("a unit that made no mutant is a grading of nothing, listed as []: %+v", got[2])
	}
	if got[4].Mutants == nil || got[4].Engine != "E" || got[4].Lang != "go" {
		t.Fatalf("an unkeyed unit still says what graded it: %+v", got[4])
	}
	if got[4].Counts.Lived != 1 || got[4].Counts.Killed != 1 || got[5].Counts.Lived != 1 {
		t.Fatal("a mutant no key names is still counted")
	}
}

func TestAnUntrustedRunIsNeverReusable(t *testing.T) {
	keys := []UnitKey{{Unit: "a", Hash: "h", Ranges: "r"}, {Unit: "b", Hash: "h", Ranges: "r"}}
	mutants := []ScoredMutant{{File: "b/x.go", Outcome: OutcomeTimedOut}}
	got := BuildGradings("go", "E", keys, dirOwner, mutants, false)
	if got[0].Reusable || got[0].WhyNot != WhyNotUntrusted || got[1].WhyNot != WhyNotUntrusted {
		t.Fatalf("an untrusted run's gradings: %+v", got)
	}
}

func TestRustScoredReadsTheOutcomeLists(t *testing.T) {
	got := RustScored("crates/a/src/x.rs:148:5: replace poll_ms -> i32 with -1\n",
		"\nsrc/lib.rs:2: replace + with -\n", "  src/lib.rs:3:1: replace f with ()  ", "garbage\n")
	want := []ScoredMutant{
		{File: "crates/a/src/x.rs", Line: 148, Col: 5, Op: "replace poll_ms -> i32 with -1", Outcome: OutcomeMissed},
		{File: "src/lib.rs", Line: 2, Col: 0, Op: "replace + with -", Outcome: OutcomeCaught},
		{File: "src/lib.rs", Line: 3, Col: 1, Op: "replace f with ()", Outcome: OutcomeUnviable},
		{Op: "garbage", Outcome: OutcomeTimedOut},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	g := BuildGradings("rust", "E", []UnitKey{{Unit: "src"}}, dirOwner, got, true)
	if g[0].Counts.Caught != 1 || g[0].Counts.Unviable != 1 || len(g[0].Mutants) != 0 {
		t.Fatalf("caught and unviable are counts: %+v", g[0])
	}
}

func TestGoGradingTrusted(t *testing.T) {
	for _, c := range []struct {
		state  int
		canary string
		want   bool
	}{{0, CanaryOK, true}, {1, CanaryOK, true}, {2, CanaryOK, false}, {0, CanaryUnknown, false}, {1, CanaryBroken, false}} {
		if GoGradingTrusted(c.state, c.canary) != c.want {
			t.Errorf("GoGradingTrusted(%d, %s) = %v", c.state, c.canary, !c.want)
		}
	}
}

// The scorer's per-mutant records say exactly what its counts say, in
// position order.
func TestTheScoreRecordsEveryMutantItDecided(t *testing.T) {
	report := `{"files":[
	 {"file_name":"main/m.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1}]},
	 {"file_name":"a.go","mutations":[
	  {"type":"K","status":"KILLED","line":1,"column":1},
	  {"type":"L","status":"LIVED","line":2,"column":1},
	  {"type":"N","status":"NOT COVERED","line":3,"column":1},
	  {"type":"S","status":"NOT COVERED","line":4,"column":2},
	  {"type":"F","status":"NOT COVERED","line":5,"column":16},
	  {"type":"O","status":"TIMED OUT","line":6,"column":1},
	  {"type":"V","status":"NOT VIABLE","line":7,"column":1},
	  {"type":"P","status":"SKIPPED","line":8,"column":1},
	  {"type":"Z","status":"WHO KNOWS","line":9,"column":1}]}]}`
	profile := "mode: set\nmod/a.go:4.10,5.2 1 1\nmod/a.go:3.1,3.9 1 0\n"
	noise := GoMutationNoise{"a.go:5:16": "declaration"}
	s, err := ScoreGoMutation([]byte(report), profile, "diff", 1, noise, GoMisgradedFiles{"main/m.go": true})
	if err != nil {
		t.Fatal(err)
	}
	want := []ScoredMutant{
		{File: "a.go", Line: 1, Col: 1, Op: "K", Outcome: OutcomeKilled, Status: "KILLED"},
		{File: "a.go", Line: 2, Col: 1, Op: "L", Outcome: OutcomeLived, Status: "LIVED"},
		{File: "a.go", Line: 3, Col: 1, Op: "N", Outcome: OutcomeNotCovered, Status: "NOT COVERED"},
		{File: "a.go", Line: 4, Col: 2, Op: "S", Outcome: OutcomeCoveredUnrun, Status: "COVERED-UNRUN", Disputed: true},
		{File: "a.go", Line: 5, Col: 16, Op: "F", Outcome: OutcomeForgiven, Status: "NOT COVERED", Detail: "declaration"},
		{File: "a.go", Line: 6, Col: 1, Op: "O", Outcome: OutcomeTimedOut, Status: "TIMED OUT"},
		{File: "a.go", Line: 7, Col: 1, Op: "V", Outcome: OutcomeInert, Status: "NOT VIABLE"},
		{File: "a.go", Line: 8, Col: 1, Op: "P", Outcome: OutcomeInert, Status: "SKIPPED"},
		{File: "a.go", Line: 9, Col: 1, Op: "Z", Status: "WHO KNOWS"},
		{File: "main/m.go", Line: 1, Col: 1, Op: "T", Outcome: OutcomeUngraded, Status: "KILLED"},
	}
	if !reflect.DeepEqual(s.Scored, want) {
		t.Fatalf("scored:\n%+v\nwant\n%+v", s.Scored, want)
	}
	if s.ProfileDisagrees != 1 {
		t.Fatalf("ProfileDisagrees = %d", s.ProfileDisagrees)
	}
}

func TestTheVerdictHandsBackTheScoreItSettledFrom(t *testing.T) {
	if _, _, _, s := GoMutationVerdictScored(GoMutationRun{}); s != nil {
		t.Fatal("no report, no score")
	}
	if _, _, _, s := GoMutationVerdictScored(GoMutationRun{Report: []byte("not json")}); s != nil {
		t.Fatal("an unreadable report, no score")
	}
	report := []byte(`{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1}]}]}`)
	state, _, _, s := GoMutationVerdictScored(GoMutationRun{Report: report, Canary: CanaryOK, MainCanary: CanaryOK})
	if state != 0 || s == nil || s.Killed != 1 || len(s.Scored) != 1 {
		t.Fatalf("state %d, score %+v", state, s)
	}
	state, _, _, s = GoMutationVerdictScored(GoMutationRun{Report: report, Status: 3})
	if state != 2 || s == nil {
		t.Fatal("a broken run's score is still handed back — its gradings are stored untrusted")
	}
}

func TestSettleStageCarriesTheGradings(t *testing.T) {
	g := []Grading{{Lang: "go", Unit: "a"}}
	st := SettleStage("mutation", []Verdict{{Atom: "go:mutation", State: 0, Result: "pass", Gradings: g}})
	if len(st.Ran) != 1 || !reflect.DeepEqual(st.Ran[0].Gradings, g) {
		t.Fatalf("the gradings stopped at the stage: %+v", st.Ran)
	}
}

func TestFoldModulesKeepsEveryModulesGradings(t *testing.T) {
	a := AtomByID("go:mutation")
	v := FoldModules(a, []ModuleVerdict{
		{Dir: ".", Verdict: Verdict{State: 0, Gradings: []Grading{{Unit: "x"}}}},
		{Dir: "tools/forge", Verdict: Verdict{State: 0}},
		{Dir: "tools/gen", Verdict: Verdict{State: 1, Gradings: []Grading{{Unit: "tools/gen/a"}, {Unit: "tools/gen/b"}}}},
	})
	if len(v.Gradings) != 3 || v.Gradings[0].Unit != "x" || v.Gradings[2].Unit != "tools/gen/b" {
		t.Fatalf("gradings = %+v", v.Gradings)
	}
}
