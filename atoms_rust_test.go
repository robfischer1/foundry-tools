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
	if strings.Contains(c, `"cargo","fetch","--locked"`) {
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
		[]string{"withExec", `args:["cargo","fetch","--locked"]`},
		[]string{"withExec", `expect:ANY`,
			`args:["cargo","clippy","--workspace","--all-targets","--","-W","clippy::all","-D","warnings"]`},
	)
	if hasCall(c, "withExec", `args:["cargo","fetch","--locked"]`, "expect:ANY") {
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
	engine.fail(`"cargo","fetch","--locked"`, "failed to download `deranged v0.5.8`")
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
		[]string{"withExec", `args:["cargo","fetch","--locked"]`},
		[]string{"withExec", `expect:ANY`, `args:["cargo","test","--workspace","--","--list"]`},
	)
	run := engine.chain(`args:["cargo","test","--workspace"]`, "exitCode")
	wantCalls(t, run,
		[]string{"withExec", `args:["cargo","fetch","--locked"]`},
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

// A linked worktree's `.git` is a FILE whose gitdir does not exist in here. A
// test that probes the repository it runs in (cerberus's porosity probe) read
// "not a repository" from the pre-push while the door's whole clone passed. The
// suite runs in gitReady's snapshot: the /src swap, then the throwaway
// repository, then cargo test.
func TestRustCargoTestRunsInATreeGitCanRead(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{".git": "gitdir: /x/.git/worktrees/y\n"}, ".git/HEAD"))
	engine.stdout(`"--list"`, "a::b: test\n")

	wantState(t, runAtom(t, "rust:cargo-test", ""), 0)

	run := engine.chain(`args:["cargo","test","--workspace"]`, "exitCode")
	wantCalls(t, run,
		[]string{"withExec", `args:["git","init","-q","."]`},
		[]string{"withExec", `args:["git","add","-A"]`},
		[]string{"withExec", `expect:ANY`, `args:["cargo","test","--workspace"]`},
	)
	swap := lastCall(run, "withMountedDirectory", `path:"/src"`)
	gitInit := strings.Index(run, `"git","init"`)
	suite := strings.Index(run, `"cargo","test","--workspace"]`)
	if swap < 0 || gitInit < 0 || suite < 0 || swap > gitInit || gitInit > suite {
		t.Errorf("the /src swap, the snapshot and the suite must run in that order (swap %d, init %d, suite %d):\n%s",
			swap, gitInit, suite, run)
	}

	// A primary checkout's `.git` is a directory: nothing is rebuilt.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"--list"`, "a::b: test\n")
	wantState(t, runAtom(t, "rust:cargo-test", ""), 0)
	if hasCall(engine.chain(`args:["cargo","test","--workspace"]`, "exitCode"), "withExec", `"git","init"`) {
		t.Errorf("a primary checkout needs no throwaway repository")
	}
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
		[]string{"withExec", `args:["cargo","fetch","--locked"]`},
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

		c := engine.chain(`args:["cargo","fetch","--locked"]`)
		if (c != "") != tc.fetches {
			t.Errorf("%s fetches the dependency graph = %v, want %v", tc.id, c != "", tc.fetches)
		}
		if c != "" && hasCall(c, "withExec", `args:["cargo","fetch","--locked"]`, "expect:ANY") {
			t.Errorf("%s runs the fetch under expect:ANY; rule 1 says a registry outage is a could-not-run:\n%s", tc.id, c)
		}
	}
}

// rust:mutation's needles: each exec the atom runs, by the words only it has.
const (
	rustBaseNeedle    = `"git","rev-parse","--verify","--quiet","abc123^{commit}"`
	rustDiffNeedle    = `"--relative","since0","HEAD"`
	rustFilesNeedle   = `"--name-only","-z"`
	rustMetaNeedle    = `"cargo","metadata","--no-deps"`
	rustMutantsNeedle = `"-D","/tmp/mutation/pr.diff"`
	// rustOneCrate is cargo metadata for a single-package crate at /src.
	rustOneCrate = `{"packages":[{"name":"x","id":"x","manifest_path":"/src/Cargo.toml"}],"workspace_members":["x"]}`
	rustDiff     = "diff --git a/src/lib.rs b/src/lib.rs\n--- a/src/lib.rs\n+++ b/src/lib.rs\n@@ -1 +1 @@\n-fn f() -> i32 { 1 }\n+fn f() -> i32 { 2 }"
)

// scriptRustMutation answers a pull that added rust lines to a one-crate
// workspace, and a cargo mutants run that caught every viable mutant. The repo
// declares no critical modules (no answers file) unless tree hands it one.
func scriptRustMutation(tree map[string]string) {
	engine.reset()
	base := rustTSTree(map[string]string{
		"/src/mutants.out/caught.txt": "src/lib.rs:1:1: replace f -> i32 with 0\n",
	})
	delete(base, ".copier-answers.yml")
	engine.withTree(base)
	engine.withTree(tree)
	engine.stdout(mergeBaseNeedle, sinceSha+"\n")
	engine.stdout(rustDiffNeedle, rustDiff)
	engine.stdout(rustFilesNeedle, "src/lib.rs\x00")
	engine.stdout(rustMetaNeedle, rustOneCrate)
}

// rust:mutation runs git, cargo metadata and cargo mutants as plain execs from
// the fetched layer, with the gate's shared target directory taken away, and
// no script: nothing mounts foundry-stocks and nothing runs bash.
func TestRustMutationMeasuresTheDiffFromTheFetchedLayer(t *testing.T) {
	scriptRustMutation(map[string]string{".copier-answers.yml": "critical_modules: src/lib.rs\n"})
	wantState(t, runAtom(t, "rust:mutation", "abc123"), 0)

	c := engine.chain(rustMutantsNeedle, "exitCode")
	if !strings.Contains(c, checks.ImageRust) {
		t.Errorf("rust:mutation must run in the rust lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withExec", `args:["cargo","fetch","--locked"]`},
		[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"abc123"`},
		[]string{"withExec", `args:["cargo","mutants","--version"]`},
		[]string{"withNewFile", `path:"/tmp/mutation/pr.diff"`},
		[]string{"withDirectory", `path:"/tmp/mutation/tmp"`},
		[]string{"withEnvVariable", `name:"TMPDIR"`, `value:"/tmp/mutation/tmp"`},
		// mold links the mutants, nextest runs them, and the dev/test profiles
		// carry no debuginfo — the per-mutant cost, not the gate (bellows
		// #31a47b3: 14s build + 30s test per mutant before this).
		[]string{"withEnvVariable", `name:"CARGO_PROFILE_DEV_DEBUG"`, `value:"0"`},
		[]string{"withEnvVariable", `name:"CARGO_PROFILE_TEST_DEBUG"`, `value:"0"`},
		[]string{"withExec", "expect:ANY", `args:["mold","-run","cargo","mutants","--colors","never","-j","2","--build-timeout","900","--minimum-test-timeout","60","--test-tool","nextest","-f","src/lib.rs","-D","/tmp/mutation/pr.diff"]`},
	)
	if hasCall(c, "withExec", `args:["cargo","mutants","--version"]`, "expect:ANY") {
		t.Errorf("the cargo-mutants probe is provisioning and must run under the default Expect:\n%s", c)
	}
	// The diff cargo mutants reads is the one git printed, whole.
	if !strings.Contains(c, `+fn f() -> i32 { 2 }\n"`) {
		t.Errorf("pr.diff is not the pull's diff with its final newline:\n%s", c)
	}
	// THE COPIES BUILD IN THEIR OWN TARGET DIRECTORIES. cargo's artifact
	// hash is workspace-relative, so cargo-mutants' parallel copies sharing
	// the gate's /cache/cargo-target ran each other's test binaries and the
	// gate ran theirs (foundry-tools#8869). The lane's variable and its mount
	// are both removed AFTER the fetch layer.
	wantCalls(t, c,
		[]string{"withoutEnvVariable", `name:"CARGO_TARGET_DIR"`},
		[]string{"withoutMount", `path:"/cache/cargo-target"`},
	)
	fetch := lastCall(c, "withExec", `args:["cargo","fetch","--locked"]`)
	drop := lastCall(c, "withoutEnvVariable", `name:"CARGO_TARGET_DIR"`)
	if fetch < 0 || drop < 0 || drop < fetch {
		t.Errorf("the target dir must be dropped after the fetch layer, not before it:\n%s", c)
	}
	for _, relic := range []string{`path:"/stocks"`, `"bash"`, "MUT_"} {
		if strings.Contains(c, relic) {
			t.Errorf("the ported lane still carries %s:\n%s", relic, c)
		}
	}
	// A one-crate workspace names no package.
	if strings.Contains(c, `"-p"`) {
		t.Errorf("a single-package crate passes no -p:\n%s", c)
	}
	// The diff is taken over the declared modules, and the file list over the
	// same pathspec, deletions left out.
	wantCalls(t, engine.chain(rustDiffNeedle, "stdout"),
		[]string{"withExec", "expect:ANY", `args:["git","diff","--relative","since0","HEAD","--","src/lib.rs"]`})
	wantCalls(t, engine.chain(rustFilesNeedle, "stdout"),
		[]string{"withExec", "expect:ANY", `args:["git","diff","--relative","--name-only","-z","--diff-filter=d","since0","HEAD","--","src/lib.rs"]`})
}

// Survivors are findings with the table and the list; the lists are read off
// mutants.out in the tree cargo mutants ran in.
func TestRustMutationSettlesWhatItMeasured(t *testing.T) {
	scriptRustMutation(map[string]string{
		".copier-answers.yml":         "critical_modules: src/lib.rs\n",
		"/src/mutants.out/missed.txt": "src/lib.rs:1:1: replace f -> i32 with 1\n",
	})
	engine.exitCode(rustMutantsNeedle, 2)
	wantState(t, runAtom(t, "rust:mutation", "abc123"), 1,
		"scoped to the declared critical modules: src/lib.rs",
		"1 viable mutant(s) survived the suite",
		"| 1 | 1 | 0 | 0 | 50% of 2 viable |",
		"src/lib.rs:1:1: replace f -> i32 with 1")

	scriptRustMutation(nil)
	engine.exitCode(rustMutantsNeedle, 4)
	engine.stdout(rustMutantsNeedle, "Found 3 mutants\nERROR cargo test failed in an unmutated tree\n")
	wantState(t, runAtom(t, "rust:mutation", "abc123"), 2,
		"no critical modules declared",
		"cargo mutants exited 4", "ERROR cargo test failed in an unmutated tree")
}

// AN EMPTY DECLARATION IS NOT AN OPT-OUT: the whole diff's rust sources are the
// scope, no -f narrows the run, and every workspace member the pull touched
// is passed as -p.
func TestRustMutationScopesAnUndeclaredPullToTheWholeDiffAndItsMembers(t *testing.T) {
	scriptRustMutation(nil)
	engine.stdout(rustFilesNeedle, "src/lib.rs\x00tools/gen/src/lib.rs\x00")
	engine.stdout(rustMetaNeedle, `{"packages":[`+
		`{"name":"x","id":"x","manifest_path":"/src/Cargo.toml"},`+
		`{"name":"gen","id":"gen","manifest_path":"/src/tools/gen/Cargo.toml"}],`+
		`"workspace_members":["x","gen"]}`)
	wantState(t, runAtom(t, "rust:mutation", "abc123"), 0)

	wantCalls(t, engine.chain(rustDiffNeedle, "stdout"),
		[]string{"withExec", `args:["git","diff","--relative","since0","HEAD","--","*.rs",":!tests/"]`})
	c := engine.chain(rustMutantsNeedle, "exitCode")
	wantCalls(t, c, []string{"withExec", `"--test-tool","nextest","-p","gen","-p","x","-D"`})
	if strings.Contains(c, `"-f"`) {
		t.Errorf("an empty declaration narrows nothing with -f:\n%s", c)
	}

	// The value is the answers file's, one leading and one trailing quote
	// stripped, and each module is its own pathspec and its own -f.
	scriptRustMutation(map[string]string{".copier-answers.yml": "other: 1\ncritical_modules: 'src/lib.rs src/gate.rs'\n"})
	engine.exitCode(rustMutantsNeedle, 2)
	wantState(t, runAtom(t, "rust:mutation", "abc123"), 1,
		"scoped to the declared critical modules: src/lib.rs src/gate.rs")
	wantCalls(t, engine.chain(rustMutantsNeedle, "exitCode"),
		[]string{"withExec", `"-f","src/lib.rs","-f","src/gate.rs","-D"`})
}

func TestRustMutationStandsDownOrCannotRun(t *testing.T) {
	cases := map[string]struct {
		base   string
		script func()
		state  int
		// reason is checked only off a could-not-run: a pass keeps no output
		// (checks.VerdictOf), so a stand-down is told apart by what it ran.
		reason  []string
		reached []string
		never   []string
	}{
		"no base": {"", nil, 0, nil, nil, []string{rustBaseNeedle, `args:["cargo","fetch","--locked"]`}},
		"a base the history lacks": {"abc123", func() { engine.exitCode(rustBaseNeedle, 1) }, 0, nil,
			[]string{rustBaseNeedle}, []string{mergeBaseNeedle, rustDiffNeedle}},
		"no merge base": {"abc123", func() { engine.exitCode(mergeBaseNeedle, 1) }, 2,
			[]string{"no merge base between the base abc123 and HEAD (exit 1)"}, []string{mergeBaseNeedle}, []string{rustDiffNeedle}},
		"no rust changed": {"abc123", func() { engine.stdout(rustDiffNeedle, "") }, 0, nil,
			[]string{rustDiffNeedle}, []string{rustFilesNeedle}},
		"lines only removed": {"abc123", func() {
			engine.stdout(rustDiffNeedle, "--- a/src/lib.rs\n+++ b/src/lib.rs\n@@ -1,2 +1 @@\n fn f() {}\n-fn g() {}")
		}, 0, nil, []string{rustDiffNeedle}, []string{rustFilesNeedle}},
		"git cannot diff": {"abc123", func() { engine.exitCode(rustDiffNeedle, 1) }, 2,
			[]string{"git could not diff the pull against its base since0"}, nil, []string{rustFilesNeedle}},
		"git cannot list the files": {"abc123", func() { engine.exitCode(rustFilesNeedle, 1) }, 2,
			[]string{"git could not list the files the pull changed"}, nil, []string{rustMetaNeedle}},
		"cargo metadata fails": {"abc123", func() {
			engine.exitCode(rustMetaNeedle, 101)
			engine.stdout(rustMetaNeedle, "error: failed to parse manifest")
		}, 2, []string{"cargo metadata failed", "failed to parse manifest"}, nil, nil},
		"cargo metadata is not json": {"abc123", func() { engine.stdout(rustMetaNeedle, "warning: x") }, 2,
			[]string{"could not map the diff onto the workspace members", "did not parse"}, nil, nil},
		"no cargo-mutants in the image": {"abc123", func() {
			engine.fail(`"cargo","mutants","--version"`, `exec: "cargo-mutants": not found`)
		}, 2, []string{"never ran", "cargo-mutants"}, nil, nil},
		"the base check never ran": {"abc123", func() { engine.fail(rustBaseNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{mergeBaseNeedle}},
		"the merge base never ran": {"abc123", func() { engine.fail(mergeBaseNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{rustDiffNeedle}},
		"the diff never ran":       {"abc123", func() { engine.fail(rustDiffNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{rustFilesNeedle}},
		"the file list never ran":  {"abc123", func() { engine.fail(rustFilesNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{rustMetaNeedle}},
		"cargo metadata never ran": {"abc123", func() { engine.fail(rustMetaNeedle, "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"cargo mutants never ran": {"abc123", func() { engine.failLeaf(rustMutantsNeedle, "stderr", "engine gone") }, 2,
			[]string{"never ran"}, nil, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			scriptRustMutation(nil)
			if c.script != nil {
				c.script()
			}
			wantState(t, runAtom(t, "rust:mutation", c.base), c.state, c.reason...)
			if name != "cargo mutants never ran" && engine.chain(rustMutantsNeedle) != "" {
				t.Errorf("settled before the mutate, and still went on to cargo mutants")
			}
			for _, n := range c.reached {
				if engine.chain(n) == "" {
					t.Errorf("never reached %s", n)
				}
			}
			for _, n := range c.never {
				if engine.chain(n) != "" {
					t.Errorf("went on to %s", n)
				}
			}
		})
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
// the release profile, --locked, into the container's own target directory —
// or the binaries the record declares, one exec each.
func TestRustReleaseBuildsWhatTheImageWillCarry(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": copiesTron, ".copier-answers.yml": "service_name: tron\n"})
	wantState(t, runAtom(t, "rust:release", ""), 0, "release build: tron", "the star's own name", "--locked")
	c := engine.chain(`"cargo","build","--release"`, "exitCode")
	wantCalls(t, c,
		[]string{"withExec", `args:["cargo","fetch","--locked"]`},
		[]string{"withEnvVariable", `name:"CARGO_TARGET_DIR"`, `value:"/work/target"`},
		[]string{"withExec", `expect:ANY`, `args:["cargo","build","--release","--locked","-p","tron"]`},
	)
	// The dependencies are provisioned before the compile (the lane's own
	// rule: one fetch, then the build reads), and the target directory is
	// the container's — set AFTER the lane's cache mounts named the cache
	// volume's, so the override is the one cargo sees.
	fetch, target, build := strings.Index(c, `"cargo","fetch","--locked"`), strings.LastIndex(c, `name:"CARGO_TARGET_DIR"`), strings.Index(c, `"cargo","build","--release"`)
	if !(fetch < target && target < build) {
		t.Errorf("fetch, then the target override, then the build — got %d %d %d:\n%s", fetch, target, build, c)
	}
	if strings.Contains(c[target:build], `value:"/cache/cargo-target"`) {
		t.Errorf("the cache volume's target directory is set after the override:\n%s", c)
	}

	// The record names more than one, and each gets its own exec.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile":                           "FROM x\nCOPY release/cerberus /cerberus\nCOPY release/cerberus-admin /cerberus-admin\n",
		".copier-answers.yml":                  "service_name: cerberus\n",
		"/dies/fleet/stars/cerberus/slag.json": `{"tools":{"build":{"binaries":["cerberus","cerberus-admin"]}}}`,
	})
	wantState(t, runAtom(t, "rust:release", ""), 0, "cerberus, cerberus-admin", "tools.build.binaries")
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
		"Dockerfile":                    "FROM x\nCOPY release/a /a\nCOPY release/b /b\n",
		".copier-answers.yml":           "service_name: a\n",
		"/dies/fleet/stars/a/slag.json": `{"tools":{"build":{"binaries":["a","b"]}}}`,
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
		"Dockerfile": copiesTron, ".copier-answers.yml": "service_name: tron\n",
		"/dies/fleet/stars/tron/slag.json": `{"tools":{"build":{"binaries":["../escape"]}}}`,
	})
	wantState(t, runAtom(t, "rust:release", ""), 2, "is not a binary name")
}

// The atoms that build into the shared foundry-cargo-target run under the
// Unstale stamp, after the fetch and before cargo; the mutation lane, which
// builds into target dirs of its own, does not pay for it.
func TestRustSharedTargetBuildsRunUnderTheStamp(t *testing.T) {
	stamp := []string{"withExec", `args:["find",".","-path","./.git","-prune"`, `"touch","-c","-d","@4102444800"`}
	for _, tc := range []struct{ atom, needle string }{
		{"rust:cargo-clippy", `"cargo","clippy"`},
		{"rust:cargo-test", `"cargo","test","--workspace","--","--list"`},
		{"rust:cargo-test", `"cargo","test","--workspace"]`},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.stdout(`"--list"`, "tests::one: test\n")
		runAtom(t, tc.atom, "")
		c := engine.chain(tc.needle, "exitCode")
		if c == "" {
			t.Fatalf("%s: no chain ran %s", tc.atom, tc.needle)
		}
		wantCalls(t, c, []string{"withExec", `args:["cargo","fetch","--locked"]`}, stamp)
		if strings.Index(c, `"cargo","fetch","--locked"`) > strings.Index(c, `"find"`) {
			t.Errorf("%s: the stamp follows the fetch:\n%s", tc.atom, c)
		}
	}

	scriptRustMutation(map[string]string{".copier-answers.yml": "critical_modules: src/lib.rs\n"})
	runAtom(t, "rust:mutation", "abc123")
	if c := engine.chain(rustMutantsNeedle, "exitCode"); hasCall(c, stamp[0], stamp[1:]...) {
		t.Errorf("rust:mutation builds in its own target dirs and must not rebuild the workspace per mutant:\n%s", c)
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
		`"uv","python","install","--default","` + checks.FleetPython + `"`,
		`UV_PYTHON_BIN_DIR`,
		`"python3","--version"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the rust lane must provision the fleet's python; missing %s", want)
		}
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
