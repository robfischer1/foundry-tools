package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

type fixedToken struct {
	value string
	err   error
}

func (f fixedToken) Plaintext(context.Context) (string, error) { return f.value, f.err }

var reuseKeys = []checks.UnitKey{{Unit: "internal/x", Hash: "hx", Ranges: "rx"}}

// A lookup over the door posts the keys under the run's token and believes
// only an answer about them; every other outcome is an error, which grades
// cold.
func TestALookupAsksTheDoorUnderTheRunsToken(t *testing.T) {
	var auth, body, path, kind string
	answer, status := `{"gradings":[{"lang":"go","unit":"internal/x","hash":"hx","ranges":"rx","run_number":7}],"misses":[]}`, http.StatusOK
	door := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		auth, body, path, kind = r.Header.Get("Authorization"), string(b), r.URL.Path, r.Header.Get("Content-Type")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	defer door.Close()
	look := lookupVia(door.URL+"/rob/ares.git", fixedToken{value: "tok"})
	got, err := look(context.Background(), "go", "E", reuseKeys)
	if err != nil || got["internal/x"].RunNumber != 7 {
		t.Fatalf("got %+v %v", got, err)
	}
	if auth != "Bearer tok" || path != "/ci/mutants" || kind != "application/json" || body != string(checks.LookupBody("go", "E", reuseKeys)) {
		t.Fatalf("the door saw %q %q %q %q", auth, path, kind, body)
	}

	status, answer = http.StatusUnauthorized, "that token names no mutation run tok"
	if _, err := look(context.Background(), "go", "E", reuseKeys); err == nil || !strings.Contains(err.Error(), "→ 401") ||
		strings.Contains(err.Error(), "tok\n") || !strings.Contains(err.Error(), "<token>") {
		t.Fatalf("a refusal: %v", err)
	}
	status, answer = http.StatusOK, `{"gradings":[{"lang":"go","unit":"elsewhere","hash":"hx","ranges":"rx"}]}`
	if _, err := look(context.Background(), "go", "E", reuseKeys); err == nil {
		t.Fatal("an answer about another unit was believed")
	}
	for name, l := range map[string]gradingLookup{
		"an empty token, which the door refuses": lookupVia(door.URL+"/x.git", fixedToken{}),
		"no repo URL":                            lookupVia("", fixedToken{value: "tok"}),
		"an unreadable token":                    lookupVia(door.URL, fixedToken{err: errors.New("no secret")}),
		"an empty token":                         lookupVia(door.URL, fixedToken{}),
		"a door that is gone":                    lookupVia("http://127.0.0.1:1/x.git", fixedToken{value: "tok"}),
		"a bad URL":                              lookupVia("http://door\x7f/x.git", fixedToken{value: "tok"}),
	} {
		if got, err := l(context.Background(), "go", "E", reuseKeys); err == nil || got != nil {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
	if ep := mutantsEndpoint("https://git.notusmi.com/rob/ares.git"); ep != "https://git.notusmi.com/ci/mutants" {
		t.Fatalf("endpoint = %q", ep)
	}
	if ep := mutantsEndpoint(""); ep != "/ci/mutants" {
		t.Fatalf("no repo URL: %q", ep)
	}
}

func TestReusableAsksOnlyWhenArmed(t *testing.T) {
	r := newRun(dag.Directory(), "", "")
	if got, note := r.reusable(context.Background(), "go", "E", reuseKeys); got != nil || note != "" {
		t.Fatal("a run with no lookup asks nothing")
	}
	r.withLookup(func(context.Context, string, string, []checks.UnitKey) (map[string]checks.ReusedGrading, error) {
		return nil, errors.New("erebus down")
	})
	if got, note := r.reusable(context.Background(), "go", "E", reuseKeys); got != nil || !strings.Contains(note, "graded cold — erebus down") {
		t.Fatalf("a lookup that failed: %v %q", got, note)
	}
}

// GateFile arms the lookup only under --reuse with a token to ask with.
func TestGateFileArmsTheLookupUnderReuse(t *testing.T) {
	for _, c := range []struct {
		reuse, token, armed bool
	}{{false, false, false}, {true, false, false}, {false, true, false}, {true, true, true}} {
		m := gateOn(t, cleanVector)
		var tok = dag.SetSecret("record-token", "tok")
		if !c.token {
			tok = nil
		}
		if _, err := m.GateFile(context.Background(), fakeTree, gatePin, "base-sha", "mutation", tok, c.reuse); err != nil {
			t.Fatal(err)
		}
		if (m.lookup != nil) != c.armed {
			t.Errorf("reuse=%v token=%v: armed %v", c.reuse, c.token, m.lookup != nil)
		}
	}
}

const twoUnitTree = "100644 blob b1\tgo.mod\x00100644 blob b2\ta.go\x00100644 blob b3\tinternal/x/x.go\x00"

// scriptTwoUnits is a pull that changed the root package and internal/x.
func scriptTwoUnits() {
	scriptGoMutation(nil)
	engine.stdout(goDiffNeedle, "a.go\ninternal/x/x.go\n")
	engine.stdout(lsTreeNeedle, twoUnitTree)
	engine.stdout(keyDiff, "+++ b/a.go\n@@ -1 +1 @@\n+x\n+++ b/internal/x/x.go\n@@ -1 +1 @@\n+y\n")
	engine.stdout(depsNeedle, "m\t/src\t\nm/internal/x\t/src/internal/x\t")
}

// runWithLookup runs go:mutation with a lookup that answers what reuse holds
// for the units it is asked about.
func runWithLookup(t *testing.T, answer func(keys []checks.UnitKey) (map[string]checks.ReusedGrading, error)) checks.Verdict {
	t.Helper()
	r := newRun(dag.Directory(), "", "abc123").withLookup(func(_ context.Context, lang, engine string, keys []checks.UnitKey) (map[string]checks.ReusedGrading, error) {
		if lang != "go" || engine != goMutationEngine() {
			t.Fatalf("asked as %s %s", lang, engine)
		}
		return answer(keys)
	})
	return registry["go:mutation"](context.Background(), r)
}

// hitFor answers a stored grading for one unit, at the key it was asked.
func hitFor(unit string, g checks.ReusedGrading) func([]checks.UnitKey) (map[string]checks.ReusedGrading, error) {
	return func(keys []checks.UnitKey) (map[string]checks.ReusedGrading, error) {
		for _, k := range keys {
			if k.Unit == unit {
				g.Lang, g.Unit, g.Hash, g.Ranges = "go", k.Unit, k.Hash, k.Ranges
				return map[string]checks.ReusedGrading{unit: g}, nil
			}
		}
		return nil, nil
	}
}

// A unit a stored grading answers is not graded again: the cover step and
// gomutants are handed only the misses, the verdict folds the reused unit in,
// and the run stores gradings only for what it graded.
func TestAReuseRunGradesOnlyItsMisses(t *testing.T) {
	scriptTwoUnits()
	v := runWithLookup(t, hitFor("internal/x", checks.ReusedGrading{Lane: "mutation", RunNumber: 9, Counts: checks.GradingCounts{Generated: 3, Killed: 3}}))
	wantState(t, v, 0, "go:mutation: reused 1 of 2 unit(s), graded earlier at the same content, scope and engine: internal/x (mutation #9 @)")
	wantCalls(t, engine.chain(goMutantsNeedle, "exitCode"), []string{"withExec", `"-changed-since","since0","."]`})
	wantCalls(t, engine.chain(goMutantsNeedle, "exitCode"), []string{"withExec", `"-coverprofile","mutation-cover.out","."]`})
	if len(v.Gradings) != 1 || v.Gradings[0].Unit != "." {
		t.Fatalf("the run stores only what it graded: %+v", v.Gradings)
	}
}

// Every unit answered: nothing runs, and the verdict is the stored one's —
// a stored survivor is still a survivor.
func TestARunWhoseEveryUnitIsReusedRunsNothing(t *testing.T) {
	scriptGoMutation(nil)
	engine.stdout(lsTreeNeedle, "100644 blob b1\tgo.mod\x00100644 blob b2\ta.go\x00")
	engine.stdout(depsNeedle, "m\t/src\t")
	lived := checks.ScoredMutant{File: "a.go", Line: 1, Col: 1, Op: "T", Outcome: checks.OutcomeLived, Status: "LIVED"}
	v := runWithLookup(t, hitFor(".", checks.ReusedGrading{Lane: "mutation", RunNumber: 3, Counts: checks.GradingCounts{Generated: 2, Killed: 1, Lived: 1},
		Mutants: []checks.ScoredMutant{lived}}))
	wantState(t, v, 1, "1 mutant(s) survived", "### Reused — 1 unit(s)")
	if len(v.Findings) != 1 || v.Findings[0].Subject != "a.go:1" || len(v.Gradings) != 0 {
		t.Fatalf("findings %+v gradings %+v", v.Findings, v.Gradings)
	}
	if engine.chain(goMutantsNeedle, "exitCode") != "" || engine.chain(goCanaryNeedle, "contents") != "" {
		t.Fatal("a run with nothing to grade ran the mutants or the controls")
	}
}

// A store that does not answer grades every unit, and says so.
func TestAStoreThatDoesNotAnswerGradesCold(t *testing.T) {
	scriptTwoUnits()
	v := runWithLookup(t, func([]checks.UnitKey) (map[string]checks.ReusedGrading, error) {
		return nil, errors.New("POST → 502")
	})
	wantState(t, v, 0, "reuse: the store did not answer, so every unit was graded cold — POST → 502")
	wantCalls(t, engine.chain(goMutantsNeedle, "exitCode"), []string{"withExec", `"-changed-since","since0","./..."]`})
	if len(v.Gradings) != 2 {
		t.Fatalf("a cold run stores every unit it graded: %+v", v.Gradings)
	}
}
