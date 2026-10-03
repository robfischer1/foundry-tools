package main

import (
	"context"
	"reflect"
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

func TestTheRustEngineIsPinned(t *testing.T) {
	const want = "1c46a081bfa87c12cd086d2849e6fe7192d36e85babc319c330e5a6e8962c9eb"
	if got := rustMutationEngine(); got != want {
		t.Fatalf("rustMutationEngine() = %s, want %s — an input moved; every stored Rust grading now misses. If that is meant, pin the new value", got, want)
	}
}

const rustTree = "100644 blob m\tCargo.toml\x00100644 blob l\tCargo.lock\x00100644 blob s\tsrc/lib.rs\x00"

// The Rust lane keys the crate its diff touched and hands back its grading,
// trusted only when the run measured.
func TestRustMutationGradesTheCrateItTouched(t *testing.T) {
	scriptRustMutation(map[string]string{"/src/mutants.out/missed.txt": "src/lib.rs:1:1: replace f -> i32 with 1\n"})
	engine.exitCode(rustMutantsNeedle, 2)
	engine.stdout(lsTreeNeedle, rustTree)
	v := runAtom(t, "rust:mutation", "abc123")
	wantState(t, v, 1)
	if len(v.Gradings) != 1 {
		t.Fatalf("gradings = %+v", v.Gradings)
	}
	g := v.Gradings[0]
	entries := checks.ParseLsTree(rustTree)
	if g.Unit != "." || g.Lang != "rust" || g.Engine != rustMutationEngine() || !g.Reusable ||
		g.Counts.Caught != 1 || g.Counts.Missed != 1 || len(g.Mutants) != 1 || g.Mutants[0].Op != "replace f -> i32 with 1" ||
		g.Hash != unitkey.Hash(unitkey.Rust, entries, []string{"."}) ||
		g.Ranges != unitkey.Ranges(unitkey.Rust, entries, ".", map[string][]unitkey.Range{"src/lib.rs": {{Start: 1, End: 1}}}) {
		t.Fatalf("grading = %+v", g)
	}
	wantCalls(t, engine.chain(rustMutantsNeedle, "exitCode"), []string{"withExec", `"--build-timeout","900","--minimum-test-timeout","60"`})

	scriptRustMutation(nil)
	engine.exitCode(rustMutantsNeedle, 4)
	engine.stdout(lsTreeNeedle, rustTree)
	v = runAtom(t, "rust:mutation", "abc123")
	if len(v.Gradings) != 1 || v.Gradings[0].Reusable || v.Gradings[0].WhyNot != checks.WhyNotUntrusted {
		t.Fatalf("a run that could not measure is stored untrusted: %+v", v.Gradings)
	}
}

// A repository whose mutants config tests the whole workspace keys each crate
// over the whole workspace.
func TestAWideMutantsConfigWidensEveryClosure(t *testing.T) {
	scriptRustMutation(map[string]string{".cargo/mutants.toml": "test_workspace = true\n"})
	engine.stdout(rustFilesNeedle, "a/src/lib.rs\x00")
	engine.stdout(rustMetaNeedle, `{"packages":[{"name":"a","id":"a","manifest_path":"/src/a/Cargo.toml","dependencies":[]},`+
		`{"name":"b","id":"b","manifest_path":"/src/b/Cargo.toml","dependencies":[{"name":"a","path":"/src/a"}]}],"workspace_members":["a","b"]}`)
	tree := "100644 blob w\tCargo.toml\x00100644 blob am\ta/Cargo.toml\x00100644 blob a\ta/src/lib.rs\x00100644 blob bm\tb/Cargo.toml\x00100644 blob b\tb/src/lib.rs\x00"
	engine.stdout(lsTreeNeedle, tree)
	v := runAtom(t, "rust:mutation", "abc123")
	// The fixture's caught mutant sits in src/lib.rs, which no crate of this
	// tree owns: it lands in an unkeyable grading of its own, after a's.
	if len(v.Gradings) != 2 || v.Gradings[0].Unit != "a" || !reflect.DeepEqual(v.Gradings[0].Closure, []string{"a", "b"}) || v.Gradings[1].Reusable {
		t.Fatalf("gradings = %+v", v.Gradings)
	}
}

// Metadata that does not parse keys nothing — the atom has already refused
// such a run, and the keys say so on their own.
func TestRustKeysOverUnreadableMetadataAreUnkeyable(t *testing.T) {
	engine.reset()
	engine.stdout(lsTreeNeedle, rustTree)
	keys, _ := rustGradingKeys(context.Background(), dag.Container(), rustDiff, "src/lib.rs", "not json", "")
	if len(keys) != 1 || keys[0].Err == "" {
		t.Fatalf("keys = %+v", keys)
	}
}
