package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE TS LANE'S CHAINS against the paper engine (engine_fake_test.go): what is
// left here is ts:mutation. ts:bun-gate lives in atoms/ts/bun-gate and is
// tested beside its file.

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
