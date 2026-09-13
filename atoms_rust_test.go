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

// cargo fmt --all --check is the one rust atom that compiles nothing: rustfmt
// parses, so a registry it never reaches cannot make it red.
func TestRustCargoFmtCompilesNothingAndFetchesNothing(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "rust:cargo-fmt", ""), 0)

	c := engine.chain(`"cargo","fmt"`, "exitCode")
	if !strings.Contains(c, checks.ImageRust) {
		t.Errorf("rust:cargo-fmt must run in the rust lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"CI"`, `value:"true"`},
		[]string{"withMountedCache", `path:"/usr/local/cargo/registry"`},
		[]string{"withMountedCache", `path:"/usr/local/cargo/git"`},
		[]string{"withMountedCache", `path:"/cache/cargo-target"`},
		[]string{"withEnvVariable", `name:"CARGO_TARGET_DIR"`, `value:"/cache/cargo-target"`},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", `expect:ANY`, `args:["cargo","fmt","--all","--check"]`},
	)
	if strings.Contains(c, `"cargo","fetch"`) {
		t.Errorf("rustfmt parses; nothing may fetch the dependency graph for it:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("rust:cargo-fmt must not read GATE_BASE — it would key the cache on the pull:\n%s", c)
	}

	// rustfmt's own vocabulary: 1 for a file it would have rewritten.
	engine.exitCode(`"cargo","fmt"`, 1)
	engine.stdout(`"cargo","fmt"`, "Diff in /src/src/lib.rs at line 3")
	wantState(t, runAtom(t, "rust:cargo-fmt", ""), 1, "Diff in /src/src/lib.rs")

	// cargo's: 101 means the command failed, and CargoExit says that is a finding.
	engine.exitCode(`"cargo","fmt"`, 101)
	wantState(t, runAtom(t, "rust:cargo-fmt", ""), 1)

	// Everything else stays a could-not-run.
	engine.exitCode(`"cargo","fmt"`, 137)
	wantState(t, runAtom(t, "rust:cargo-fmt", ""), 2)
	engine.exitCode(`"cargo","fmt"`, 127)
	wantState(t, runAtom(t, "rust:cargo-fmt", ""), 2)

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"cargo","fmt"`, "failed to pull rust-ci: 404")
	wantState(t, runAtom(t, "rust:cargo-fmt", ""), 2, "never ran", "404")
}

// The fleet's lint set is named on the command line, after `--`, so a crate's
// own [workspace.lints.clippy] table cannot narrow it.
func TestRustCargoClippyNamesTheFleetsLintSetAfterTheSeparator(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "rust:cargo-clippy", ""), 0)

	c := engine.chain(`"cargo","clippy"`, "exitCode")
	wantCalls(t, c,
		[]string{"withExec", `args:["cargo","fetch"]`},
		[]string{"withExec", `expect:ANY`,
			`args:["cargo","clippy","--workspace","--all-targets","--","-W","clippy::all","-D","warnings"]`},
	)
	if hasCall(c, "withExec", `args:["cargo","fetch"]`, "expect:ANY") {
		t.Errorf("the fetch is provisioning and must run under the default Expect:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("rust:cargo-clippy must not read GATE_BASE:\n%s", c)
	}

	// A denied lint fails the BUILD, and cargo reports a failed build as 101.
	// Reading that as could-not-run is the whole reason CargoExit exists.
	engine.exitCode(`"cargo","clippy"`, 101)
	engine.stderr(`"cargo","clippy"`, "error: this expression creates a reference immediately dereferenced")
	wantState(t, runAtom(t, "rust:cargo-clippy", ""), 1, "immediately dereferenced")

	engine.exitCode(`"cargo","clippy"`, 1)
	wantState(t, runAtom(t, "rust:cargo-clippy", ""), 1)

	engine.exitCode(`"cargo","clippy"`, 137)
	wantState(t, runAtom(t, "rust:cargo-clippy", ""), 2)

	// Rule 1: a registry outage is the engine's, not the committer's.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"cargo","fetch"`, "failed to download `deranged v0.5.8`")
	wantState(t, runAtom(t, "rust:cargo-clippy", ""), 2, "never ran", "deranged")
}

// cargo test asks the toolchain what tests exist BEFORE it runs them: a
// workspace that lists none is red before the suite builds, and a listing that
// would not build is the committer's to fix.
func TestRustCargoTestListsTheSuiteBeforeItRunsIt(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"--list"`, "checks::lane::works: test\nbench::throughput: benchmark\n\n1 test, 1 benchmark\n")

	wantState(t, runAtom(t, "rust:cargo-test", ""), 0)

	list := engine.chain(`"--list"`, "exitCode")
	wantCalls(t, list,
		[]string{"withExec", `args:["cargo","fetch"]`},
		[]string{"withExec", `expect:ANY`, `args:["cargo","test","--workspace","--","--list"]`},
	)
	run := engine.chain(`args:["cargo","test","--workspace"]`, "exitCode")
	wantCalls(t, run,
		[]string{"withExec", `args:["cargo","fetch"]`},
		[]string{"withExec", `expect:ANY`, `args:["cargo","test","--workspace"]`},
	)
	if strings.Contains(run, "--list") {
		t.Errorf("the suite run is its own chain off the fetched layer, not the listing's:\n%s", run)
	}

	// The suite itself, in cargo's vocabulary.
	engine.exitCode(`args:["cargo","test","--workspace"]`, 101)
	engine.stdout(`args:["cargo","test","--workspace"]`, "test lane::works ... FAILED")
	wantState(t, runAtom(t, "rust:cargo-test", ""), 1, "FAILED")

	engine.exitCode(`args:["cargo","test","--workspace"]`, 1)
	wantState(t, runAtom(t, "rust:cargo-test", ""), 1)

	engine.exitCode(`args:["cargo","test","--workspace"]`, 137)
	wantState(t, runAtom(t, "rust:cargo-test", ""), 2)
}

func TestRustCargoTestReadsTheListingsOwnAnswers(t *testing.T) {
	// A listing that would not BUILD is a finding naming the first rustc
	// diagnostic — not a could-not-run, and not the whole 400-line fallout.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"--list"`, 101)
	engine.stdout(`"--list"`, "   Compiling x v0.1.0\nwarning: unused import\n")
	engine.stderr(`"--list"`, "error[E0425]: cannot find value `nope` in this scope\nerror: aborting due to 1 previous error\n")
	wantState(t, runAtom(t, "rust:cargo-test", ""), 1,
		"the tests did not build", "error[E0425]: cannot find value `nope`")
	if v := runAtom(t, "rust:cargo-test", ""); strings.Contains(v.Reason, "aborting due to") {
		t.Errorf("only the FIRST diagnostic is named; the rest is fallout:\n%s", v.Reason)
	}
	if engine.chain(`args:["cargo","test","--workspace"]`) != "" {
		t.Error("a listing that would not build must not run the suite")
	}

	// A build that would not build and prints nothing matching still files the
	// finding: the exit code already decided it.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"--list"`, 101)
	engine.stdout(`"--list"`, "the linker exploded\n")
	wantState(t, runAtom(t, "rust:cargo-test", ""), 1, "the tests did not build")

	// libtest exits 0 and prints "running 0 tests" for a workspace with no
	// #[test]. That is the green this atom exists to refuse.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"--list"`, "\n0 tests, 0 benchmarks\n")
	wantState(t, runAtom(t, "rust:cargo-test", ""), 1, "no test in the workspace")
	if engine.chain(`args:["cargo","test","--workspace"]`) != "" {
		t.Error("a workspace with no test must not run the suite")
	}

	// A benchmark is not a test.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"--list"`, "bench::throughput: benchmark\n\n0 tests, 1 benchmark\n")
	wantState(t, runAtom(t, "rust:cargo-test", ""), 1, "no test in the workspace")

	// Both evaluations are the engine's to fail, and both are state 2.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"--list"`, "engine went away")
	wantState(t, runAtom(t, "rust:cargo-test", ""), 2, "never ran", "engine went away")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"--list"`, "a::b: test\n")
	engine.fail(`args:["cargo","test","--workspace"]`, "the mount would not evaluate")
	wantState(t, runAtom(t, "rust:cargo-test", ""), 2, "never ran", "would not evaluate")
}

// cargo-audit is an EXTERNAL subcommand: cargo execs it and passes its status
// through, so its code reaches the verdict unmapped — 1 is an advisory, 101 is
// cargo failing to run it at all.
func TestRustCargoAuditPassesItsOwnExitCodeThroughRaw(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "rust:cargo-audit", ""), 0)

	c := engine.chain(`args:["cargo","audit"]`, "exitCode")
	wantCalls(t, c,
		[]string{"withExec", `args:["cargo","fetch"]`},
		[]string{"withExec", `args:["cargo","audit","--version"]`},
		[]string{"withExec", `expect:ANY`, `args:["cargo","audit"]`},
	)
	if hasCall(c, "withExec", `args:["cargo","audit","--version"]`, "expect:ANY") {
		t.Errorf("the version probe is provisioning and must run under the default Expect:\n%s", c)
	}
	// cargo-audit is provisioned ONCE, pinned, by the lane — never by the
	// atom.
	if !strings.Contains(c, `"cargo","install","cargo-audit","--locked","--version","`+checks.CargoAuditVersion+`"`) || strings.Count(c, `"cargo","install","cargo-audit"`) != 1 {
		t.Errorf("the lane provisions cargo-audit at its pin, once:\n%s", c)
	}

	engine.exitCode(`args:["cargo","audit"]`, 1)
	engine.stdout(`args:["cargo","audit"]`, "error: 1 vulnerability found! RUSTSEC-2025-0001")
	wantState(t, runAtom(t, "rust:cargo-audit", ""), 1, "RUSTSEC-2025-0001")

	// NOT remapped by CargoExit: a 101 here is cargo failing to exec the
	// subcommand, which is a could-not-run and must read as one.
	engine.exitCode(`args:["cargo","audit"]`, 101)
	wantState(t, runAtom(t, "rust:cargo-audit", ""), 2, "exit 101")

	engine.exitCode(`args:["cargo","audit"]`, 127)
	wantState(t, runAtom(t, "rust:cargo-audit", ""), 2)

	// An image that lost the binary is state 2 with the exec's own error,
	// never a green from a tool that never ran.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"cargo","audit","--version"`, "exec: \"cargo-audit\": not found")
	wantState(t, runAtom(t, "rust:cargo-audit", ""), 2, "never ran", "cargo-audit")
}

// ONE FETCH, SHARED. cargo's package-cache lock lives in CARGO_HOME, which the
// cache mount does not cover, so two containers resolving the registry
// concurrently do not see each other's lock (measured 2026-09-12 on tongs).
// Every atom that compiles branches from the fetched layer; the one that does
// not compile must not pay for it.
func TestCargoDepsIsTheBaseOfEveryCompilingAtom(t *testing.T) {
	for _, tc := range []struct {
		id      string
		fetches bool
	}{
		{"rust:cargo-fmt", false},
		{"rust:cargo-clippy", true},
		{"rust:cargo-test", true},
		{"rust:cargo-audit", true},
		{"rust:mutation", true},
	} {
		engine.reset()
		engine.withTree(rustTSTree(map[string]string{
			"/tmp/mutation/verdict": "0\n",
			"/tmp/mutation/reason":  "every mutant killed",
		}))
		engine.stdout(`"--list"`, "a::b: test\n")
		runAtom(t, tc.id, "abc123")

		c := engine.chain(`args:["cargo","fetch"]`)
		if (c != "") != tc.fetches {
			t.Errorf("%s fetches the dependency graph = %v, want %v", tc.id, c != "", tc.fetches)
		}
		if c != "" && hasCall(c, "withExec", `args:["cargo","fetch"]`, "expect:ANY") {
			t.Errorf("%s runs the fetch under expect:ANY; rule 1 says a registry outage is a could-not-run:\n%s", tc.id, c)
		}
	}
}

// rust:mutation is rustTSMutation with the rust spec: the fetched layer as its
// base, two probes, three phases.
func TestRustMutationRunsItsThreePhasesFromTheFetchedLayer(t *testing.T) {
	engine.reset()
	engine.withTree(rustTSTree(map[string]string{
		".copier-answers.yml":   "critical_modules: src/lib.rs\n",
		"/tmp/mutation/verdict": "0\n",
		"/tmp/mutation/reason":  "every viable mutant killed",
	}))

	wantState(t, runAtom(t, "rust:mutation", "abc123"), 0)

	c := engine.chain(`rust.sh","score"`, "exitCode")
	if !strings.Contains(c, checks.ImageRust) {
		t.Errorf("rust:mutation must run in the rust lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withExec", `args:["cargo","fetch"]`},
		[]string{"withMountedDirectory", `path:"/stocks"`},
		[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"abc123"`},
		[]string{"withEnvVariable", `name:"MUT_BASE"`, `value:"abc123"`},
		[]string{"withEnvVariable", `name:"MUT_MODE"`, `value:"diff"`},
		[]string{"withEnvVariable", `name:"MUT_DIR"`, `value:"/tmp/mutation"`},
		[]string{"withEnvVariable", `name:"MUT_MODULES"`, `value:"src/lib.rs"`},
		[]string{"withExec", `args:["bash","--version"]`},
		[]string{"withExec", `args:["cargo","mutants","--version"]`},
	)
	for _, probe := range []string{`args:["bash","--version"]`, `args:["cargo","mutants","--version"]`} {
		if hasCall(c, "withExec", probe, "expect:ANY") {
			t.Errorf("%s is provisioning and must run under the default Expect:\n%s", probe, c)
		}
	}
	// THE COPIES BUILD IN THEIR OWN TARGET DIRECTORIES. cargo's artifact
	// hash is workspace-relative, so cargo-mutants' parallel copies sharing
	// the gate's /cache/cargo-target ran each other's test binaries and the
	// gate ran theirs (foundry-tools#8869). The lane's variable and its mount
	// are both removed AFTER the fetch layer, and the job count is the
	// engine's two, not rust.sh's four.
	wantCalls(t, c,
		[]string{"withoutEnvVariable", `name:"CARGO_TARGET_DIR"`},
		[]string{"withoutMount", `path:"/cache/cargo-target"`},
		[]string{"withEnvVariable", `name:"MUT_JOBS"`, `value:"2"`},
	)
	fetch := lastCall(c, "withExec", `args:["cargo","fetch"]`)
	drop := lastCall(c, "withoutEnvVariable", `name:"CARGO_TARGET_DIR"`)
	if fetch < 0 || drop < 0 || drop < fetch {
		t.Errorf("the target dir must be dropped after the fetch layer, not before it:\n%s", c)
	}
	for _, phase := range []string{"resolve", "mutate", "score"} {
		if !hasCall(c, "withExec", "expect:ANY", `"/stocks/ci/lib/mutation/rust.sh","`+phase+`"`) {
			t.Errorf("rust:mutation lacks phase %s under ANY:\n%s", phase, c)
		}
	}
	// cargo-mutants does its own build; the go and ts scripts' phases are not
	// this script's, and a phase this atom names that rust.sh does not define
	// would be a CANNOT RUN on every run.
	for _, notAPhase := range []string{"setup", "cover", "install", "build", "teardown"} {
		if strings.Contains(c, `rust.sh","`+notAPhase+`"`) {
			t.Errorf("rust:mutation runs a phase rust.sh does not define: %s\n%s", notAPhase, c)
		}
	}

	// The score phase's verdict file IS the answer, and the reason file is the
	// sentence printed with it.
	engine.withTree(map[string]string{
		"/tmp/mutation/verdict": "1\n",
		"/tmp/mutation/reason":  "3 mutants survived",
	})
	wantState(t, runAtom(t, "rust:mutation", "abc123"), 1,
		"3 mutants survived", "scoped to the declared critical modules: src/lib.rs")
}

func TestRustMutationCannotRunPaths(t *testing.T) {
	base := map[string]string{
		"/tmp/mutation/verdict": "0\n",
		"/tmp/mutation/reason":  "every viable mutant killed",
	}

	// A phase that exits non-zero is a broken script, said as CANNOT RUN and
	// NAMING THE PHASE — the reason each phase is its own evaluated exec.
	for _, phase := range []string{"resolve", "mutate", "score"} {
		engine.reset()
		engine.withTree(rustTSTree(base))
		engine.exitCode(`rust.sh","`+phase+`"`, 1)
		engine.stdout(`rust.sh","`+phase+`"`, "cargo mutants: no such option --in-diff")
		wantState(t, runAtom(t, "rust:mutation", "abc123"), 2,
			"phase "+phase, "no such option")
	}

	// A missing toolchain is a Dagger error on the provisioning probe.
	engine.reset()
	engine.withTree(rustTSTree(base))
	engine.fail(`"cargo","mutants","--version"`, "exec: \"cargo-mutants\": not found")
	wantState(t, runAtom(t, "rust:mutation", "abc123"), 2, "never ran", "cargo-mutants")

	// The script read at its one home. Absent means foundry-stocks did not
	// mount — an engine fact, not a repo one — and nothing else runs.
	engine.reset()
	engine.withTree(rustTSTree(base))
	engine.fail("ci/lib/mutation/rust.sh", "no such file or directory")
	wantState(t, runAtom(t, "rust:mutation", "abc123"), 2,
		"/stocks/ci/lib/mutation/rust.sh is absent", "foundry-stocks did not mount")
	if engine.chain(`args:["cargo","fetch"]`) != "" {
		t.Error("an unmountable stocks tree must not start a container")
	}

	// No verdict file: the score phase measured nothing, which is not a pass.
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "rust:mutation", "abc123"), 2, "no verdict")

	// A verdict that is not an integer is the same refusal.
	engine.reset()
	engine.withTree(rustTSTree(map[string]string{"/tmp/mutation/verdict": "clean\n"}))
	wantState(t, runAtom(t, "rust:mutation", "abc123"), 2, `wrote "clean"`, "which is not a verdict")

	// A verdict with no sentence attached says so rather than rendering
	// "<atom>: " and nothing after it.
	engine.reset()
	engine.withTree(rustTSTree(map[string]string{"/tmp/mutation/verdict": "1\n"}))
	wantState(t, runAtom(t, "rust:mutation", "abc123"), 1, "wrote verdict 1 and no reason")
}

// AN EMPTY DECLARATION IS NOT AN OPT-OUT, and the scope line says which of the
// two happened either way.
func TestRustMutationPrintsItsScopeWithOrWithoutADeclaration(t *testing.T) {
	engine.reset()
	tree := rustTSTree(map[string]string{
		"/tmp/mutation/verdict": "1\n",
		"/tmp/mutation/reason":  "1 mutant survived",
	})
	delete(tree, ".copier-answers.yml")
	engine.withTree(tree)

	v := runAtom(t, "rust:mutation", "abc123")
	wantState(t, v, 1, "no critical modules declared", "the whole diff is the scope")
	if !hasCall(engine.chain(`rust.sh","score"`, "exitCode"), "withEnvVariable", `name:"MUT_MODULES"`, `value:""`) {
		t.Error("an absent answers file is an empty declaration, not an absent variable")
	}

	// The value is the answers file's, one leading and one trailing quote
	// stripped — the shell's sed, exactly.
	engine.reset()
	engine.withTree(rustTSTree(map[string]string{
		".copier-answers.yml":   "other: 1\ncritical_modules: 'src/lib.rs src/gate.rs'\n",
		"/tmp/mutation/verdict": "1\n",
		"/tmp/mutation/reason":  "1 mutant survived",
	}))
	wantState(t, runAtom(t, "rust:mutation", "abc123"), 1,
		"scoped to the declared critical modules: src/lib.rs src/gate.rs")
}

// A repo with no Cargo.toml never reaches a runner: the lane is a fact about
// the tree, and its absence costs one entries read rather than a container.
func TestTheRustLaneIsAbsentWithoutACargoToml(t *testing.T) {
	for _, id := range []string{
		"rust:cargo-fmt", "rust:cargo-clippy", "rust:cargo-test",
		"rust:cargo-audit", "rust:mutation",
	} {
		engine.reset()
		engine.withTree(map[string]string{"package.json": "{}"})
		v, err := verdictFor(t.Context(), newRun(dag.Directory(), ""), id)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "no Cargo.toml") {
			t.Errorf("%s: want an absent 0 naming Cargo.toml, got %+v", id, v)
		}
		if len(engine.chains()) != 1 {
			t.Errorf("%s: an absent lane must cost one entries read, not a container: %d queries",
				id, len(engine.chains()))
		}
	}
}
