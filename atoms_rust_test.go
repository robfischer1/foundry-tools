package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE RUST LANE against the paper engine (engine_fake_test.go), following
// atoms_go_test.go's exemplar: each atom gets its happy path with the chain
// read back, EVERY decision it makes about cargo's answer, and the
// engine-error path.
//
// The lane's one structural claim is cargoDeps — the dependency graph fetched
// once, on its own layer, by every atom that COMPILES and by none that does
// not — and TestCargoDepsIsTheBaseOfEveryCompilingAtom holds it for all five
// at once rather than leaving it to five happy paths to agree by accident.

// rustTSTree is everyLaneTree with files added or replaced, without mutating
// the shared map. A mutation atom's verdict and reason files are declared by
// their absolute container path, which is how the paper engine serves a file
// read off a container.
func rustTSTree(extra map[string]string) map[string]string {
	tree := make(map[string]string, len(everyLaneTree)+len(extra))
	for k, v := range everyLaneTree {
		tree[k] = v
	}
	for k, v := range extra {
		tree[k] = v
	}
	return tree
}

// ONE FETCH, SHARED. cargo's package-cache lock lives in CARGO_HOME, which the
// cache mount does not cover, so two containers resolving the registry
// concurrently do not see each other's lock (measured 2026-09-12 on tongs).
// Every atom that compiles branches from the fetched layer; the one that does
// not compile must not pay for it. rust:mutation fetches only once it knows it
// will mutate something (TestRustMutationMeasuresTheDiffFromTheFetchedLayer
// holds the fetch, TestRustMutationWithNothingToMutateSettlesBeforeProvisioning
// its absence).
func TestCargoDepsIsTheBaseOfEveryCompilingAtom(t *testing.T) {
	for _, tc := range []struct {
		id      string
		fetches bool
	}{
		{"rust:cargo-fmt", true},
		{"rust:cargo-clippy", true},
		{"rust:cargo-test", true},
		{"rust:cargo-audit", true},
	} {
		engine.reset()
		engine.withTree(rustTSTree(map[string]string{
			"/tmp/mutation/verdict": "0\n",
			"/tmp/mutation/reason":  "every mutant killed",
		}))
		engine.stdout(`"--list"`, "a::b: test\n")
		runAtom(t, tc.id, "abc123")

		c := engine.chain(`args:["cargo","fetch","--locked"]`)
		if (c != "") != tc.fetches {
			t.Errorf("%s fetches the dependency graph = %v, want %v", tc.id, c != "", tc.fetches)
		}
		// the ported atoms run the toolchain's preflight (tools.toml) under expect:ANY
		// and read its exit: a registry outage is still a could-not-run (atom_env.go)
		ported := tc.id == "rust:cargo-fmt" || tc.id == "rust:cargo-audit"
		if c != "" && hasCall(c, "withExec", `args:["cargo","fetch","--locked"]`, "expect:ANY") != ported {
			t.Errorf("%s runs the fetch under expect:ANY; rule 1 says a registry outage is a could-not-run:\n%s", tc.id, c)
		}
	}
}

// A repo with no Cargo.toml never reaches a runner: the lane is a fact about
// the tree, the PLANNER reads that fact once before anything starts
// (run.plan), and the atom answers absent without a container.
func TestTheRustLaneIsAbsentWithoutACargoToml(t *testing.T) {
	for _, id := range []string{
		"rust:cargo-fmt", "rust:cargo-clippy", "rust:cargo-test",
		"rust:cargo-audit", "rust:mutation",
	} {
		engine.reset()
		engine.withTree(map[string]string{"package.json": "{}"})
		vs, err := (&FoundryTools{Source: dag.Directory()}).vector(t.Context(), checks.AtomByID(id).Stage, id, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(vs) != 1 {
			t.Fatalf("%s: one atom asked, %d answered", id, len(vs))
		}
		v := vs[0]
		if v.Atom != id || v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "no Cargo.toml") {
			t.Errorf("%s: want an absent 0 naming Cargo.toml, got %+v", id, v)
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

// THE RUST LANE EXECUTES THE FLEET'S PYTHON, so it has to carry the fleet's
// interpreter. cerberus' memory_plugin.rs spawns .cerberus/hooks/*.py from
// `cargo test`, and rust:bookworm's own python3 is 3.11.2 — measured
// 2026-09-25, the day `ruff format` under target-version py314 emitted PEP 758
// into those hooks and turned five of those tests into a SyntaxError.
//
// Asserted on the CHAIN rather than by running a container: what this pins is
// that the lane provisions an interpreter at all and names the version once,
// which is the thing a later edit would silently drop.
func TestTheRustLaneCarriesTheFleetsPython(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": "[package]\nname='x'\n"})
	if _, err := (&FoundryTools{Source: dag.Directory()}).vector(t.Context(), checks.AtomByID("rust:cargo-fmt").Stage, "rust:cargo-fmt", ""); err != nil {
		t.Fatal(err)
	}
	c := engine.chains()
	joined := strings.Join(c, "\n")
	for _, want := range []string{
		`"tar","xzf","/tmp/python.tar.gz","-C","` + pythonInstallDir + `"`,
		`"ln","-sf","` + pythonInstallDir + `/bin/python3","/usr/local/bin/python3"`,
		`UV_PYTHON_DOWNLOADS`,
		`"python3","--version"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the rust lane must provision the fleet's python; missing %s", want)
		}
	}
	// uv resolves versions only from its compiled-in manifest; a pin ahead of
	// it is CANNOT RUN on every rust lane (foundry-tools #326).
	if strings.Contains(joined, `"uv","python","install"`) {
		t.Error("the rust lane must not ask uv to install python")
	}
}

// ONE VERSION, NAMED ONCE. The python image and the rust lane's uv install are
// two places the same interpreter is chosen, and a drift between them is
// exactly the split this constant exists to close.
func TestTheFleetsPythonMatchesThePythonImage(t *testing.T) {
	if !strings.Contains(checks.ImagePython, "python:"+checks.FleetPython) {
		t.Errorf("ImagePython (%s) does not carry FleetPython (%s)", checks.ImagePython, checks.FleetPython)
	}
}

// The target volume is mounted with default (shared) sharing; the registry and git caches, which are
// content-addressed, are not.
func TestRustLaneTargetVolumeIsShared(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "rust:cargo-fmt", ""), 0)
	c := engine.chain(`"cargo","fmt"`, "exitCode")
	wantCalls(t, c, []string{"withMountedCache", `path:"/cache/cargo-target"`})
	if hasCall(c, "withMountedCache", `path:"/cache/cargo-target"`, `sharing:`) {
		t.Errorf("the target volume is shared within a run:\n%s", c)
	}
}

// The fetch is the one step that serialises across containers (cargo's flock
// in the shared CARGO_HOME), and it sweeps stale git-db locks first, under
// that same flock. Neither the volume nor any exec is LOCKED.
func TestCargoFetchSweepsStaleLocksFirstAndLocksNothing(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents(rustClippyNeedle, rustVerdict("rust:cargo-clippy", "precommit"))
	runAtom(t, "rust:cargo-clippy", "")
	c := engine.chain(rustClippyNeedle, "contents")

	sweep := strings.Index(c, `"flock","/cache/cargo-home/.package-cache"`)
	fetch := strings.Index(c, `"cargo","fetch","--locked"`)
	if sweep < 0 || fetch < 0 || sweep > fetch {
		t.Errorf("the stale-lock sweep must run before the fetch (sweep %d, fetch %d):\n%s", sweep, fetch, c)
	}
	if strings.Contains(c, "sharing:LOCKED") && mountSharingAt(c, "/cache/cargo-home", atomExec) == "LOCKED" {
		t.Errorf("the cargo home must not be LOCKED:\n%s", c)
	}
}

// A python pin with no sum is not fetched unverified: the rust lane gets an exec
// that cannot succeed, the same fail-closed rule as every other pinned tool.
func TestTheRustLanesPythonWithoutAPinnedSumFailsClosed(t *testing.T) {
	engine.reset()
	engine.fail(`"/bin/false"`, "exit code: 1")
	c := withPythonStandalone(dag.Container().From("x"), map[string]string{})
	if _, err := c.Sync(t.Context()); err == nil {
		t.Error("a rust lane with no pinned python sum provisioned anyway")
	}
	if engine.chain(`http(`, checks.PythonStandaloneURL) != "" {
		t.Error("python was fetched with nothing to verify it against")
	}
}
