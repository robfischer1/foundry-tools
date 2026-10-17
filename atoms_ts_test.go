package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE TS LANE'S CHAINS against the paper engine (engine_fake_test.go): what is
// left here is ts:mutation. ts:bun-gate lives in atoms/ts/bun-gate and is
// tested beside its file.

// ts:mutation's needles: each exec the atom runs, by the words only it has.
const (
	tsBaseNeedle    = `"git","rev-parse","--verify","--quiet","abc123^{commit}"`
	tsDiffNeedle    = `"git","diff","--unified=0","since0","HEAD"`
	tsInstallNeedle = `args:["bun","install","--frozen-lockfile"]`
	tsFindNeedle    = `"find","/src","-type","f"`
	tsStrykerNeedle = `"--reporters","clear-text,json"`
	tsGlobNeedle    = `glob(pattern:"**/*stryker.con*")`
	tsDiff          = "diff --git a/src/gate.ts b/src/gate.ts\n--- a/src/gate.ts\n+++ b/src/gate.ts\n@@ -1,0 +2,2 @@\n+a\n+b"
)

const tsKilled = `{"status":"Killed","mutatorName":"BooleanLiteral","testsCompleted":1,"location":{"start":{"line":2}}}`

// A repo with no package.json never reaches a runner: the lane is a fact about
// the tree, the PLANNER reads that fact once before anything starts
// (run.plan), and the atom answers absent without a container.
func TestTheTSLaneIsAbsentWithoutAPackageJson(t *testing.T) {
	for _, id := range []string{
		"ts:bun-gate", "ts:bun-audit", "ts:mutation",
	} {
		engine.reset()
		engine.withTree(map[string]string{"Cargo.toml": "{}"})
		vs, err := (&FoundryTools{Source: dag.Directory()}).vector(t.Context(), checks.AtomByID(id).Stage, id, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(vs) != 1 {
			t.Fatalf("%s: one atom asked, %d answered", id, len(vs))
		}
		v := vs[0]
		if v.Atom != id || v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "no package.json") {
			t.Errorf("%s: want an absent 0 naming package.json, got %+v", id, v)
		}
		// THREE READS FOR THE WHOLE PLAN — one entries read, one go.mod walk
		// and one .py walk (2026-09-25: the python lane is declared by its
		// files too, so the planner asks about them the way it asks about Go
		// modules). The number this pins is CONSTANT PER RUN: never a
		// container, and never a read per atom.
		if len(engine.chains()) > 3 {
			t.Errorf("%s: an absent lane costs the plan's reads, not a container: %d queries",
				id, len(engine.chains()))
		}
	}
}
