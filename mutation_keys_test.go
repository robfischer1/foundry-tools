package main

import (
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/unitkey"
)

// THE ENGINE ID IS PINNED. Any input to it moving — a tool, an image, a flag,
// the epoch — moves this value, and every stored grading misses: correct, and
// a decision, so it fails here until someone writes the new value down.
func TestTheGoEngineIsPinned(t *testing.T) {
	const want = "64f90d39b74d389044d7f10eca9681fb5390f3a47215d4b35363e528b04ffe42"
	if got := goMutationEngine(); got != want {
		t.Fatalf("goMutationEngine() = %s, want %s — an input moved; every stored Go grading now misses. If that is meant, pin the new value", got, want)
	}
}

const (
	lsTreeNeedle = `"git","-C","/src","ls-tree"`
	keyDiff      = `"git","-C","/src","diff","--unified=0"`
	depsNeedle   = `"go","list","-e","-deps","-test"`
)

// The Go lane keys the units its diff touched and hands back one grading per
// unit, built from the score it settled on.
func TestGoMutationGradesEachUnitItTouched(t *testing.T) {
	scriptGoMutation(nil)
	engine.stdout(lsTreeNeedle, "100644 blob b1\tgo.mod\x00100644 blob b2\ta.go\x00100644 blob b3\tinternal/x/x.go\x00")
	engine.stdout(keyDiff, "+++ b/a.go\n@@ -1 +1 @@\n+x\n")
	engine.stdout(depsNeedle, "m\t/src\tm/internal/x\nm/internal/x\t/src/internal/x\t\n")
	v := runAtom(t, "go:mutation", "abc123")
	wantState(t, v, 0)
	if len(v.Gradings) != 1 {
		t.Fatalf("gradings = %+v", v.Gradings)
	}
	g := v.Gradings[0]
	entries := checks.ParseLsTree("100644 blob b1\tgo.mod\x00100644 blob b2\ta.go\x00100644 blob b3\tinternal/x/x.go\x00")
	if g.Unit != "." || g.Lang != "go" || g.Engine != goMutationEngine() || !g.Reusable || g.Counts.Killed != 1 ||
		g.Hash != unitkey.Hash(unitkey.Go, entries, []string{".", "internal/x"}) ||
		g.Ranges != unitkey.Ranges(unitkey.Go, entries, ".", map[string][]unitkey.Range{"a.go": {{Start: 1, End: 1}}}) {
		t.Fatalf("grading = %+v", g)
	}
	wantCalls(t, engine.chain(keyDiff, "stdout"), []string{"withExec", `"git","-C","/src","diff","--unified=0","--no-color","--no-ext-diff","since0","HEAD"`})
	wantCalls(t, engine.chain(lsTreeNeedle, "stdout"), []string{"withExec", `"git","-C","/src","ls-tree","-r","-z","--full-tree","HEAD"`})
	wantCalls(t, engine.chain(depsNeedle, "stdout"), []string{"withExec", `"go","list","-e","-deps","-test","-f"`, `"./..."`})
}

// A key read that fails never touches the verdict: the units are graded and
// stored unkeyable.
func TestAKeyReadThatFailsLeavesTheVerdictAlone(t *testing.T) {
	scriptGoMutation(nil)
	engine.stdout(lsTreeNeedle, "100644 blob b2\ta.go\x00")
	engine.exitCode(depsNeedle, 1)
	v := runAtom(t, "go:mutation", "abc123")
	wantState(t, v, 0)
	if len(v.Gradings) != 1 || v.Gradings[0].Reusable || v.Gradings[0].WhyNot != checks.WhyNotUnkeyable || v.Gradings[0].Counts.Killed != 1 {
		t.Fatalf("gradings = %+v", v.Gradings)
	}
}

// A read that exits non-zero is not believed, whatever it printed.
func TestAKeyReadThatExitsWrongIsNotBelieved(t *testing.T) {
	for _, needle := range []string{keyDiff, depsNeedle, lsTreeNeedle} {
		scriptGoMutation(nil)
		engine.stdout(lsTreeNeedle, "100644 blob b1\tgo.mod\x00100644 blob b2\ta.go\x00")
		engine.stdout(keyDiff, "+++ b/a.go\n@@ -1 +1 @@\n+x\n")
		engine.stdout(depsNeedle, "m\t/src\t\n")
		engine.exitCode(needle, 2)
		v := runAtom(t, "go:mutation", "abc123")
		wantState(t, v, 0)
		if len(v.Gradings) != 1 || v.Gradings[0].WhyNot != checks.WhyNotUnkeyable {
			t.Fatalf("%s exited 2: gradings = %+v", needle, v.Gradings)
		}
	}
}

// The keys read the closure under the databases' build tags, as the run does:
// a DB-gated test file's imports are in the closure only under its tag.
func TestTheClosureIsReadUnderTheRunsTags(t *testing.T) {
	scriptGoMutation(map[string]string{
		".copier-answers.yml":           "service_name: x\n",
		"/dies/fleet/stars/x/slag.json": `{"backends":{"postgres":{}}}`,
	})
	engine.stdout(`"grep","-rhoE"`, "//go:build live_db\n")
	runAtom(t, "go:mutation", "abc123")
	wantCalls(t, engine.chain(depsNeedle, "stdout"), []string{"withExec", `"-test","-f"`, `"-tags","live_db","./..."`})
}
