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

// copiesTron is the three-line shape for a Rust star: a Dockerfile that asks
// for the Gate's artifact by copying it from release/ (buildlane.CopiesRelease).
const copiesTron = "FROM x\nCOPY release/tron /tron\n"

// THE RELEASE BUILD IS THE IMAGE'S COMPILE, derived: the star's own crate under
// the release profile, --locked, into the repository's release volume and
// copied out of it by the same exec — or the binaries the Dockerfile copies,
// one exec each.
func TestRustReleaseBuildsWhatTheImageWillCarry(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": copiesTron, ".copier-answers.yml": "service_name: tron\n"})
	wantState(t, runAtom(t, "rust:release", ""), 0, "release build: tron", "the Dockerfile's COPY lines", "--locked")
	c := engine.chain(`"cargo","build","--release"`, "exitCode")
	wantCalls(t, c,
		[]string{"withExec", `args:["cargo","fetch","--locked"]`},
		[]string{"withMountedCache", `path:"/cache/cargo-release"`, `sharing:SHARED`},
		[]string{"withFile", `path:"/usr/local/bin/copyout"`},
		[]string{"withEnvVariable", `name:"CARGO_TARGET_DIR"`, `value:"/cache/cargo-release"`},
		[]string{"withExec", `expect:ANY`, `args:["/usr/local/bin/copyout","/cache/cargo-release/release/tron=/out/tron","--","cargo","build","--release","--locked","-p","tron"]`},
	)
	// The dependencies are provisioned before the compile (the lane's own
	// rule: one fetch, then the build reads), the tree is stamped so the
	// volume's artifacts of an older tree are rebuilt, and the target
	// directory is the release volume — set AFTER the lane's cache mounts
	// named the debug volume's, so the override is the one cargo sees.
	fetch, stamp := strings.Index(c, `"cargo","fetch","--locked"`), strings.Index(c, `"touch","-c","-d"`)
	target, build := strings.LastIndex(c, `name:"CARGO_TARGET_DIR"`), strings.Index(c, `"cargo","build","--release"`)
	if !(fetch < stamp && stamp < target && target < build) {
		t.Errorf("fetch, stamp, the target override, then the build — got %d %d %d %d:\n%s", fetch, stamp, target, build, c)
	}
	if strings.Contains(c[target:build], `value:"/cache/cargo-target"`) {
		t.Errorf("the cache volume's target directory is set after the override:\n%s", c)
	}

	// The Dockerfile copies more than one, and each gets its own exec.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile":          "FROM x\nCOPY release/cerberus /cerberus\nCOPY release/cerberus-admin /cerberus-admin\n",
		".copier-answers.yml": "service_name: cerberus\n",
	})
	wantState(t, runAtom(t, "rust:release", ""), 0, "cerberus, cerberus-admin", "the Dockerfile's COPY lines")
	for _, b := range []string{"cerberus", "cerberus-admin"} {
		if engine.chain(`"-p","`+b+`"`) == "" {
			t.Errorf("no exec builds %s:\n%v", b, engine.chains())
		}
	}
}

// A CRATE THAT DOES NOT COMPILE IS A FINDING IN CARGO'S VOCABULARY (101, not
// 1), reported with the compiler's own words, and the chain stops at it: a
// second binary is not built on top of a first that failed.
func TestRustReleaseReportsTheBinaryThatDidNotBuild(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile":          "FROM x\nCOPY release/a /a\nCOPY release/b /b\n",
		".copier-answers.yml": "service_name: a\n",
	})
	engine.exitCode(`"-p","a"`, 101)
	engine.stderr(`"-p","a"`, "error[E0425]: cannot find value `x` in this scope")
	wantState(t, runAtom(t, "rust:release", ""), 1, "release build: a, b", "error[E0425]")
	if engine.chain(`"-p","b"`) != "" {
		t.Errorf("b was built on top of a's failed compile:\n%v", engine.chains())
	}

	// A signal is could-not-run, as it is for every cargo atom.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": copiesTron, ".copier-answers.yml": "service_name: tron\n"})
	engine.exitCode(`"-p","tron"`, 137)
	wantState(t, runAtom(t, "rust:release", ""), 2)

	// An engine that goes away is could-not-run that says so.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": copiesTron, ".copier-answers.yml": "service_name: tron\n"})
	engine.fail(`"-p","tron"`, "the engine went away")
	wantState(t, runAtom(t, "rust:release", ""), 2, "the engine went away")
}

// THE THREE ABSENCES ARE go:release's, IN ITS ORDER: no image, no star, a
// Dockerfile that compiles itself — each a 0 that says why, none a compile.
func TestRustReleaseIsAbsentWhereGoReleaseIs(t *testing.T) {
	noBuild := func(t *testing.T) {
		t.Helper()
		if engine.chain(`"cargo","build","--release"`) != "" {
			t.Errorf("nothing compiles for an absence:\n%v", engine.chains())
		}
	}
	// A Cargo workspace and no Dockerfile ships no image.
	engine.reset()
	engine.withTree(map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n", "src/main.rs": ""})
	v := runAtom(t, "rust:release", "")
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "tracks no Dockerfile") {
		t.Errorf("want an absent 0 naming the missing Dockerfile, got %+v", v)
	}
	noBuild(t)

	// Dockerfiles and no star: not a star image. The star is read before the
	// Dockerfile's ask, so a non-star is never told it compiles itself.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\n"})
	v = runAtom(t, "rust:release", "")
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "names no star") || !strings.Contains(v.Reason, "not a star image") || strings.Contains(v.Reason, "compiles itself") {
		t.Errorf("want an absent 0 saying it is not a star image, got %+v", v)
	}
	noBuild(t)
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\n", ".copier-answers.yml": "project: x\n"})
	if v := runAtom(t, "rust:release", ""); v.Result != "absent" || !strings.Contains(v.Reason, "no service_name") {
		t.Errorf("%+v", v)
	}

	// A Dockerfile with its own build stage asks for nothing.
	self := "FROM docker.notusmi.com/library/rust:1.98 AS build\nRUN cargo build --release -p tron\nFROM x\nCOPY --from=build /src/target/release/tron /tron\n"
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": self, ".copier-answers.yml": "service_name: tron\n"})
	v = runAtom(t, "rust:release", "")
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "compiles itself") || !strings.Contains(v.Reason, "release/") {
		t.Errorf("want an absent 0 saying the image compiles itself, got %+v", v)
	}
	noBuild(t)
}

// EVERY WAY THE RELEASE BUILD CANNOT ANSWER IS A COULD-NOT-RUN THAT SAYS WHY:
// a tree it cannot read, a Dockerfile it cannot read, a record whose binaries
// are unusable.
func TestRustReleaseSaysWhyItCouldNotRun(t *testing.T) {
	image := map[string]string{"Dockerfile": copiesTron, ".copier-answers.yml": "service_name: tron\n"}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(image)
	engine.fail("glob", "the tree went away")
	wantState(t, runAtom(t, "rust:release", ""), 2, "the tree could not be read", "the tree went away")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(image)
	engine.failLeaf(`file(path:"Dockerfile")`, "contents", "the file went away")
	wantState(t, runAtom(t, "rust:release", ""), 2, "Dockerfile could not be read", "the file went away")
	if engine.chain(`"cargo","build","--release"`) != "" {
		t.Errorf("a Dockerfile that could not be read still compiled:\n%v", engine.chains())
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile": "FROM x\nCOPY release/-flag /tron\n", ".copier-answers.yml": "service_name: tron\n",
	})
	wantState(t, runAtom(t, "rust:release", ""), 2, "is not a binary name")
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
