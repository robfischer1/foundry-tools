package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE PYTHON LANE'S TESTS, against the paper engine (engine_fake_test.go) and
// shaped like atoms_go_test.go's exemplar. Each atom gets its happy path with
// the chain read back — the image, the caches, the mounts, which exec carries
// expect:ANY, rule 8's GATE_BASE — then every decision it makes about the
// tool's answer, every absence it can announce, every CANNOT RUN it can issue,
// and the engine-error path.
//
// TWO CANNOT-RUNS ARE ASKED BEFORE ANY CONTAINER STARTS and the tests hold
// that: a missing fleet ruleset and an unreadable repository root both cost
// ONE query, never a provisioned image. `len(engine.chains())` is the
// assertion — a chain that was built would be visible there.

// pyTree copies everyLaneTree, applies the overrides, and drops the paths
// named (a directory by its name, which takes everything under it). Those two
// moves are all any branch in this file needs to make the shared tree say
// something it does not say by default.
func pyTree(overrides map[string]string, drop ...string) map[string]string {
	out := map[string]string{}
	for k, v := range everyLaneTree {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	for _, d := range drop {
		for k := range out {
			if k == d || strings.HasPrefix(k, strings.TrimSuffix(d, "/")+"/") {
				delete(out, k)
			}
		}
	}
	return out
}

const (
	ruffToml = "/stocks/ci/lib/rulesets/ruff.toml"
	mypyIni  = "/stocks/ci/lib/rulesets/mypy.ini"
)

// ---- ruff ----

func TestPythonRuffCheckRunsTheFleetsRulesetAndReadsTheExit(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "python:ruff-check", "base-sha"), 0)

	c := engine.chain(`"uvx","ruff@0.16.3","check"`, "exitCode")
	if !strings.Contains(c, checks.ImagePython) {
		t.Errorf("python:ruff-check must run in the python lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"CI"`, `value:"true"`},
		[]string{"withMountedCache", `path:"/opt/uv-cache"`, `source:`},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withMountedDirectory", `path:"/stocks"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", `args:["uvx","ruff@0.16.3","--version"]`},
		[]string{"withExec", `expect:ANY`, `args:["uvx","ruff@0.16.3","check","--config","` + ruffToml + `","."]`},
	)
	// Rule 1: the resolver probe is provisioning and a resolver that could not
	// resolve is a could-not-run, so it must NOT carry anyExit.
	if hasCall(c, "withExec", `args:["uvx","ruff@0.16.3","--version"]`, `expect:ANY`) {
		t.Errorf("the uvx resolve is provisioning and must run under the default Expect:\n%s", c)
	}
	// Rule 8: ruff does not judge the change, so its cache key stays a
	// function of the tree.
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("python:ruff-check must not read GATE_BASE:\n%s", c)
	}

	// Rule 2: ruff's own exit code is the verdict.
	engine.exitCode(`"ruff@0.16.3","check"`, 1)
	engine.stdout(`"ruff@0.16.3","check"`, "src/x.py:1:1: F401 [*] `os` imported but unused")
	wantState(t, runAtom(t, "python:ruff-check", ""), 1, "F401")

	engine.exitCode(`"ruff@0.16.3","check"`, 2)
	engine.stderr(`"ruff@0.16.3","check"`, "ruff failed: invalid configuration")
	wantState(t, runAtom(t, "python:ruff-check", ""), 2, "invalid configuration")

	// The provisioning exec is the engine's error, not a finding.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"uvx","ruff@0.16.3","--version"`, "failed to resolve ruff@0.16.3")
	wantState(t, runAtom(t, "python:ruff-check", ""), 2, "never ran", "failed to resolve")
}

// A RULESET THE ATOM CANNOT READ IS A GATE THAT NEVER LOOKED. Both ruff atoms
// ask foundry-stocks for it in Go, before the mount is paid for — so the
// absence costs one query and never a container.
func TestPythonRuffCannotRunWithoutTheFleetsRuleset(t *testing.T) {
	for _, id := range []string{"python:ruff-check", "python:ruff-format"} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.fail("ci/lib/rulesets/ruff.toml", "no such file or directory")

		wantState(t, runAtom(t, id, ""), 2,
			id+": CANNOT RUN", ruffToml+" is absent", "foundry-stocks did not mount at its one home")
		if n := len(engine.chains()); n != 1 {
			t.Errorf("%s: a missing ruleset must cost one contents read, not a container: %d queries", id, n)
		}
	}
}

func TestPythonRuffFormatScopesAGoStarToItsProductPython(t *testing.T) {
	// A go star with product python: src and tests, in the shell loop's order.
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "python:ruff-format", "base-sha"), 0)

	c := engine.chain(`"uvx","ruff@0.16.3","format"`, "exitCode")
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/stocks"`},
		[]string{"withExec", `args:["uvx","ruff@0.16.3","--version"]`},
		[]string{"withExec", `expect:ANY`, `args:["uvx","ruff@0.16.3","format","--config","` + ruffToml + `","--check","src","tests"]`},
	)
	// --check, NEVER the rewrite: a formatter that rewrites a tree mid-commit
	// has aborted a commit in this fleet before.
	if strings.Contains(c, `"format","--config","`+ruffToml+`","src"`) {
		t.Errorf("ruff format must never run without --check:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("python:ruff-format must not read GATE_BASE:\n%s", c)
	}

	// A python star with no go.mod formats itself whole.
	engine.reset()
	engine.withTree(pyTree(nil, "go.mod"))
	wantState(t, runAtom(t, "python:ruff-format", ""), 0)
	if c := engine.chain(`"ruff@0.16.3","format"`, "exitCode"); !hasCall(c, "withExec", `"--check","."]`) {
		t.Errorf("a tree with no go.mod formats itself whole:\n%s", c)
	}

	// A go star with neither src/ nor tests/ has nothing to format, and says so.
	engine.reset()
	engine.withTree(pyTree(nil, "src", "tests"))
	v := runAtom(t, "python:ruff-format", "")
	wantState(t, v, 0, "python:ruff-format: ABSENT - a go star with no product python under src/ or tests/")
	if v.Result != "absent" {
		t.Errorf("want absent, got %q: %s", v.Result, v.Reason)
	}
	if c := engine.chain("withExec"); c != "" {
		t.Errorf("an absent format target must cost no container:\n%s", c)
	}

	// ruff's own exit code is the verdict.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"ruff@0.16.3","format"`, 1)
	engine.stdout(`"ruff@0.16.3","format"`, "Would reformat: src/x.py")
	wantState(t, runAtom(t, "python:ruff-format", ""), 1, "Would reformat")
}

// A TREE THE ENGINE CANNOT LIST IS A FACT ABOUT THE REPOSITORY, not a finding
// about it — and the three atoms that read the root all say so the same way.
func TestPythonAtomsCannotRunWhenTheRootWillNotList(t *testing.T) {
	for _, id := range []string{"python:ruff-format", "python:mypy", "python:pytest"} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.fail("{entries}", "engine went away")

		wantState(t, runAtom(t, id, ""), 2,
			id+": CANNOT RUN", "could not read the repository root", "engine went away")
	}
}

// ---- forge-testkit ----

// THREE HOOKS, ONE FUNCTION, THREE POPULATIONS. The mode names the linter's
// subcommand and the pattern names what pre-commit's `files:` matched, read
// over the gate's own population rather than as a git pathspec.
func TestPythonForgeTestkitLintsEachModeOverItsOwnPopulation(t *testing.T) {
	for _, tc := range []struct {
		id      string
		mode    string
		pattern string
		files   string
	}{
		{"python:forge-testkit-assertion-free", "assertion-free", "tests/**/*.py", `"tests/test_x.py"`},
		{"python:forge-testkit-fake-placement", "fake-placement", "**/*.py", `"src/x.py","tests/test_x.py","tools/check_contracts.py"`},
		{"python:forge-testkit-schema-budget", "schema-budget", "src/**/*.py", `"src/x.py"`},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)

		wantState(t, runAtom(t, tc.id, "base-sha"), 0)

		c := engine.chain(`"forge-testkit-lint","`+tc.mode+`"`, "exitCode")
		if !strings.Contains(c, checks.ImagePython) {
			t.Errorf("%s must run in the python lane image:\n%s", tc.id, c)
		}
		wantCalls(t, c,
			[]string{"withMountedCache", `path:"/opt/uv-cache"`},
			[]string{"withMountedDirectory", `path:"/src"`},
			[]string{"withWorkdir", `path:"/src"`},
			[]string{"withExec", `args:["uv","--version"]`},
			[]string{"withExec", `expect:ANY`, `args:["uv","run","--extra","dev","forge-testkit-lint","` + tc.mode + `",` + tc.files + `]`},
		)
		if hasCall(c, "withExec", `args:["uv","--version"]`, `expect:ANY`) {
			t.Errorf("%s: the uv probe is provisioning and must run under the default Expect:\n%s", tc.id, c)
		}
		// THE FILE LIST IS ARGUMENTS, NOT xargs — xargs would answer 123 for
		// the linter's own exit 1 and StateFor would read a finding as a
		// could-not-run.
		if strings.Contains(c, "xargs") {
			t.Errorf("%s: the population is the argv, never xargs:\n%s", tc.id, c)
		}
		// forge-testkit is the repo's own dependency: no ruleset, no /stocks.
		if strings.Contains(c, `path:"/stocks"`) {
			t.Errorf("%s must not mount foundry-stocks:\n%s", tc.id, c)
		}
		if strings.Contains(c, "GATE_BASE") {
			t.Errorf("%s must not read GATE_BASE:\n%s", tc.id, c)
		}

		engine.exitCode(`"forge-testkit-lint","`+tc.mode+`"`, 1)
		engine.stdout(`"forge-testkit-lint","`+tc.mode+`"`, "tests/test_x.py:1: no assertion in this test body")
		wantState(t, runAtom(t, tc.id, ""), 1, "no assertion in this test body")

		engine.exitCode(`"forge-testkit-lint","`+tc.mode+`"`, 127)
		wantState(t, runAtom(t, tc.id, ""), 2)
	}
}

// TWO ABSENCES, BOTH ANNOUNCED — plus the one CANNOT RUN the enumeration can
// issue, and the engine error the probe can raise.
func TestPythonForgeTestkitStandsDownWhereItHasNothingToRead(t *testing.T) {
	const id = "python:forge-testkit-assertion-free"

	// No pyproject.toml at all: the contents read errors, and a repo with no
	// manifest never took the dependency.
	engine.reset()
	engine.withTree(pyTree(nil, "pyproject.toml"))
	v := runAtom(t, id, "")
	wantState(t, v, 0, id+": ABSENT - forge-testkit is not a dependency of this project")
	if v.Result != "absent" {
		t.Errorf("want absent, got %q: %s", v.Result, v.Reason)
	}

	// A MENTION IS NOT AN ENTRY (Lovelace13, helios, 2026-09-10): the bare
	// word in a comment must not provision a linter the repo never installed.
	engine.reset()
	engine.withTree(pyTree(map[string]string{
		"pyproject.toml": "[project]\nname = \"x\"\n# forge-testkit is how the fleet lints tests\ndependencies = []\n",
	}))
	wantState(t, runAtom(t, id, ""), 0, id+": ABSENT - forge-testkit is not a dependency")
	if c := engine.chain("withExec"); c != "" {
		t.Errorf("an undeclared dependency must cost no container:\n%s", c)
	}

	// Declared, but no file matches: pre-commit skips a hook with an empty
	// file list, and a linter handed nothing would be a pass over nothing.
	engine.reset()
	engine.withTree(pyTree(nil, "tests"))
	wantState(t, runAtom(t, id, ""), 0, id+": ABSENT - no files match tests/**/*.py")
	if c := engine.chain("withExec"); c != "" {
		t.Errorf("an empty population must cost no container:\n%s", c)
	}

	// The enumeration itself is a CANNOT RUN when the engine will not answer.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`glob(pattern:"tests/`, "filter: engine went away")
	wantState(t, runAtom(t, id, ""), 2, id+": CANNOT RUN", "could not enumerate the repository", "engine went away")

	// And the provisioning probe is the engine's error, never a finding.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"uv","--version"`, "image would not pull")
	wantState(t, runAtom(t, id, ""), 2, "never ran", "image would not pull")
}

// ---- mypy ----

func TestPythonMypyTypeChecksTheProductDirsUnderTheFleetsConfig(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "python:mypy", "base-sha"), 0)

	c := engine.chain(`"mypy","--config-file"`, "exitCode")
	if !strings.Contains(c, checks.ImagePython) {
		t.Errorf("python:mypy must run in the python lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/stocks"`},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withExec", `args:["uv","--version"]`},
		[]string{"withExec", `expect:ANY`, `args:["uv","run","--all-extras","mypy","--config-file","` + mypyIni + `","src","tests"]`},
	)
	if hasCall(c, "withExec", `args:["uv","--version"]`, `expect:ANY`) {
		t.Errorf("the uv probe is provisioning and must run under the default Expect:\n%s", c)
	}
	// pyright is deliberately absent from this lane; one type checker, not two.
	if strings.Contains(c, "pyright") {
		t.Errorf("the python lane runs one type checker:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("python:mypy must not read GATE_BASE:\n%s", c)
	}

	// One product dir is still a target list.
	engine.reset()
	engine.withTree(pyTree(nil, "tests"))
	wantState(t, runAtom(t, "python:mypy", ""), 0)
	if c := engine.chain(`"mypy","--config-file"`, "exitCode"); !hasCall(c, "withExec", `"`+mypyIni+`","src"]`) {
		t.Errorf("a tree with only src/ types src/ alone:\n%s", c)
	}

	// Neither: nothing to type-check, announced.
	engine.reset()
	engine.withTree(pyTree(nil, "src", "tests"))
	v := runAtom(t, "python:mypy", "")
	wantState(t, v, 0, "python:mypy: ABSENT - no src/ or tests/ to type-check")
	if v.Result != "absent" {
		t.Errorf("want absent, got %q: %s", v.Result, v.Reason)
	}
	if c := engine.chain("withExec"); c != "" {
		t.Errorf("no target must cost no container:\n%s", c)
	}

	// The fleet's mypy.ini has its own home, and not reading it is a CANNOT RUN.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail("ci/lib/rulesets/mypy.ini", "no such file or directory")
	wantState(t, runAtom(t, "python:mypy", ""), 2, "python:mypy: CANNOT RUN", mypyIni+" is absent")
	if n := len(engine.chains()); n != 1 {
		t.Errorf("a missing ruleset must cost one contents read, not a container: %d queries", n)
	}

	// Rule 2, both directions.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"mypy","--config-file"`, 1)
	engine.stdout(`"mypy","--config-file"`, "src/x.py:3: error: Function is missing a return type annotation")
	wantState(t, runAtom(t, "python:mypy", ""), 1, "missing a return type annotation")

	engine.exitCode(`"mypy","--config-file"`, 2)
	wantState(t, runAtom(t, "python:mypy", ""), 2)

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"uv","--version"`, "image would not pull")
	wantState(t, runAtom(t, "python:mypy", ""), 2, "never ran", "image would not pull")
}

// ---- pytest ----

// NOTHING IS BUILT WITHOUT TESTS (Rob, 2026-09-11). The emptiness is decided
// in Go, off the tree, before pytest is asked — and it is a FINDING, not the
// ABSENT this atom used to answer.
func TestPythonPytestTreatsAnEmptyLaneAsAFinding(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "python:pytest", "base-sha"), 0)

	c := engine.chain(`"pytest","-q"`, "exitCode")
	if !strings.Contains(c, checks.ImagePython) {
		t.Errorf("python:pytest must run in the python lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedCache", `path:"/opt/uv-cache"`},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", `args:["uv","--version"]`},
		[]string{"withExec", `expect:ANY`, `args:["uv","run","--all-extras","pytest","-q"]`},
	)
	if hasCall(c, "withExec", `args:["uv","--version"]`, `expect:ANY`) {
		t.Errorf("the uv probe is provisioning and must run under the default Expect:\n%s", c)
	}
	if strings.Contains(c, `path:"/stocks"`) {
		t.Errorf("python:pytest runs the repo's own tests and mounts no ruleset:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("python:pytest must not read GATE_BASE:\n%s", c)
	}

	// EXIT 5 IS "NO TESTS RAN", and it carries its own sentence.
	engine.exitCode(`"pytest","-q"`, 5)
	wantState(t, runAtom(t, "python:pytest", ""), 1,
		"python:pytest: FINDINGS - pytest collected no tests (exit 5); nothing is built without tests")

	// Exit 1 is pytest's own report, and the tool's output is the reason.
	engine.exitCode(`"pytest","-q"`, 1)
	engine.stdout(`"pytest","-q"`, "1 failed, 2 passed")
	wantState(t, runAtom(t, "python:pytest", ""), 1, "1 failed, 2 passed")

	// EVERY OTHER NON-ZERO CODE IS ALSO A FINDING — the shell body's fold of
	// 2/3/4 to one exit 1, carried rather than re-litigated. StateFor would
	// have read these as could-not-run; PytestState is what stops it.
	for _, code := range []int{2, 3, 4, 137} {
		engine.exitCode(`"pytest","-q"`, code)
		wantState(t, runAtom(t, "python:pytest", ""), 1)
	}

	// The engine's own failure stays distinct from the tool's.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"uv","--version"`, "image would not pull")
	wantState(t, runAtom(t, "python:pytest", ""), 2, "never ran", "image would not pull")
}

func TestPythonPytestFindsTestsWithoutATestsDirectory(t *testing.T) {
	// No tests/ — but pytest's other default naming convention is satisfied,
	// so the collection is real and pytest is asked.
	engine.reset()
	engine.withTree(pyTree(map[string]string{"pkg/test_a.py": ""}, "tests"))
	wantState(t, runAtom(t, "python:pytest", ""), 0)
	if c := engine.chain(`"pytest","-q"`, "exitCode"); c == "" {
		t.Error("a test_*.py outside tests/ must still be collected")
	}

	// The second convention, *_test.py.
	engine.reset()
	engine.withTree(pyTree(map[string]string{"pkg/a_test.py": ""}, "tests"))
	wantState(t, runAtom(t, "python:pytest", ""), 0)
	if c := engine.chain(`"pytest","-q"`, "exitCode"); c == "" {
		t.Error("an *_test.py outside tests/ must still be collected")
	}

	// Neither: no tests/ and nothing matching either convention. A FINDING,
	// decided off the tree, and pytest is never provisioned to say it.
	engine.reset()
	engine.withTree(pyTree(nil, "tests"))
	v := runAtom(t, "python:pytest", "")
	wantState(t, v, 1, "python:pytest: FINDINGS - no tests/ and no test files; nothing is built without tests")
	if v.Result == "absent" {
		t.Errorf("an empty python lane is red, never absent: %+v", v)
	}
	if c := engine.chain("withExec"); c != "" {
		t.Errorf("the emptiness is decided in Go and must cost no container:\n%s", c)
	}

	// The enumeration is a CANNOT RUN when the engine will not answer it.
	engine.reset()
	engine.withTree(pyTree(nil, "tests"))
	engine.fail(`glob(pattern:"**/test_`, "filter: engine went away")
	wantState(t, runAtom(t, "python:pytest", ""), 2, "python:pytest: CANNOT RUN", "could not enumerate the repository", "engine went away")
}

// ---- pip-audit ----

func TestPythonPipAuditRunsTheFleetsAuditorNotTheReposDependency(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "python:pip-audit", "base-sha"), 0)

	c := engine.chain(`"pip-audit","pip-audit"`, "exitCode")
	if !strings.Contains(c, checks.ImagePython) {
		t.Errorf("python:pip-audit must run in the python lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedCache", `path:"/opt/uv-cache"`},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withExec", `args:["uv","--version"]`},
		// --with rather than a dependency of the repo: a repo that did not
		// declare the auditor is still audited.
		[]string{"withExec", `expect:ANY`, `args:["uv","run","--with","pip-audit","pip-audit"]`},
	)
	if hasCall(c, "withExec", `args:["uv","--version"]`, `expect:ANY`) {
		t.Errorf("the uv probe is provisioning and must run under the default Expect:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("python:pip-audit must not read GATE_BASE:\n%s", c)
	}

	engine.exitCode(`"pip-audit","pip-audit"`, 1)
	engine.stdout(`"pip-audit","pip-audit"`, "Found 1 known vulnerability in 1 package")
	wantState(t, runAtom(t, "python:pip-audit", ""), 1, "known vulnerability")

	// A 127 is a missing binary and a 137 is an OOM kill; neither is a
	// finding and neither is a pass.
	for _, code := range []int{2, 127, 137} {
		engine.exitCode(`"pip-audit","pip-audit"`, code)
		wantState(t, runAtom(t, "python:pip-audit", ""), 2)
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"uv","--version"`, "image would not pull")
	wantState(t, runAtom(t, "python:pip-audit", ""), 2, "never ran", "image would not pull")
}

// ---- mutation ----

// mutationTree is everyLaneTree plus the two files the score phase writes,
// read back off the scored container by absolute path.
func mutationTree(verdict, reason string) map[string]string {
	return pyTree(map[string]string{
		"/tmp/mutation/verdict": verdict,
		"/tmp/mutation/reason":  reason,
	})
}

func TestPythonMutationRunsEveryPhaseAndReadsTheVerdictFile(t *testing.T) {
	engine.reset()
	engine.withTree(mutationTree("0\n", "every mutant killed\n"))

	// A PASS's reason is reasonFor's "<id>: PASS" and nothing else, so the
	// scope line and the score phase's sentence are read back off a non-pass
	// verdict below; here the chain is what carries the scope.
	wantState(t, runAtom(t, "python:mutation", "abc123"), 0)

	c := engine.chain(`python.sh","score"`, "exitCode")
	if !strings.Contains(c, checks.ImagePython) {
		t.Errorf("python:mutation must run in the python lane image:\n%s", c)
	}
	wantCalls(t, c,
		// Rule 8's one exception: the mutation lane judges the change.
		[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"abc123"`},
		[]string{"withEnvVariable", `name:"MUT_BASE"`, `value:"abc123"`},
		[]string{"withEnvVariable", `name:"MUT_DIR"`, `value:"/tmp/mutation"`},
		[]string{"withEnvVariable", `name:"MUT_MODE"`, `value:"diff"`},
		[]string{"withEnvVariable", `name:"MUT_MODULES"`, `value:"src/x.py"`},
		[]string{"withMountedDirectory", `path:"/stocks"`},
		[]string{"withExec", `args:["git","config","--global","--add","safe.directory","*"]`},
		[]string{"withExec", `args:["bash","--version"]`},
		[]string{"withExec", `args:["uv","--version"]`},
	)
	// ONE EXEC PER PHASE, in the script's order, each under anyExit.
	for _, phase := range mutationPhasesPython {
		if !hasCall(c, "withExec", `expect:ANY`, `"/stocks/ci/lib/mutation/python.sh","`+phase+`"`) {
			t.Errorf("python:mutation lacks phase %s under ANY:\n%s", phase, c)
		}
	}
	// Rule 7: the script IS the tool, run as one exec — never through a shell.
	if strings.Contains(c, `args:["bash","-c"`) {
		t.Errorf("python:mutation must not exec a shell:\n%s", c)
	}

	// The verdict file is the answer, 1 is survivors, and the scope line rides
	// in front of it.
	engine.reset()
	engine.withTree(mutationTree("1\n", "3 mutants survived in src/x.py\n"))
	wantState(t, runAtom(t, "python:mutation", "abc123"), 1,
		"python:mutation: scoped to the declared critical modules: src/x.py",
		"3 mutants survived in src/x.py")

	// A verdict the score phase never wrote is a could-not-measure, never a
	// pass — foundry-stocks#4415's zero-file scan wearing another hat.
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "python:mutation", "abc123"), 2,
		"python:mutation: CANNOT RUN", "the score phase wrote no verdict")

	engine.reset()
	engine.withTree(mutationTree("   \n", "ignored"))
	wantState(t, runAtom(t, "python:mutation", "abc123"), 2, "the score phase wrote no verdict")

	engine.reset()
	engine.withTree(mutationTree("banana\n", "ignored"))
	wantState(t, runAtom(t, "python:mutation", "abc123"), 2, `wrote "banana", which is not a verdict`)

	// A verdict with no sentence attached says it arrived bare, rather than
	// rendering "<atom>: " and nothing after it.
	engine.reset()
	engine.withTree(mutationTree("1\n", "  \n"))
	wantState(t, runAtom(t, "python:mutation", "abc123"), 1, "wrote verdict 1 and no reason")

	// A reason file the score phase never wrote at all is the same shape.
	engine.reset()
	engine.withTree(pyTree(map[string]string{"/tmp/mutation/verdict": "1\n"}))
	wantState(t, runAtom(t, "python:mutation", "abc123"), 1, "wrote verdict 1 and no reason")

	// The scope line is printed with the CANNOT RUN too.
	engine.reset()
	engine.withTree(pyTree(nil, ".copier-answers.yml"))
	wantState(t, runAtom(t, "python:mutation", "abc123"), 2,
		"no critical modules declared", "the score phase wrote no verdict")
}

// AN EMPTY DECLARATION IS THE WHOLE DIFF, NOT AN OPT-OUT — and the atom says
// which of the two happened either way.
func TestPythonMutationScopesItselfFromTheCopierAnswers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answers map[string]string
		drop    []string
		mods    string
		says    string
	}{
		{"declared", map[string]string{".copier-answers.yml": "critical_modules: \"src/a src/b\"\n"}, nil,
			"src/a src/b", "scoped to the declared critical modules: src/a src/b"},
		{"declared blank", map[string]string{".copier-answers.yml": "critical_modules:   \n"}, nil,
			"", "an empty list is not an opt-out"},
		{"key absent", map[string]string{".copier-answers.yml": "_src_path: x\n"}, nil,
			"", "an empty list is not an opt-out"},
		{"no answers file at all", nil, []string{".copier-answers.yml"},
			"", "an empty list is not an opt-out"},
	} {
		engine.reset()
		// Verdict 1, because a PASS's reason is "<id>: PASS" and the scope
		// line would not be visible in it.
		tree := mutationTree("1\n", "3 mutants survived\n")
		for k, v := range tc.answers {
			tree[k] = v
		}
		for _, d := range tc.drop {
			delete(tree, d)
		}
		engine.withTree(tree)

		v := runAtom(t, "python:mutation", "abc123")
		wantState(t, v, 1, tc.says)
		c := engine.chain(`python.sh","score"`, "exitCode")
		if !hasCall(c, "withEnvVariable", `name:"MUT_MODULES"`, `value:"`+tc.mods+`"`) {
			t.Errorf("%s: MUT_MODULES must carry %q:\n%s", tc.name, tc.mods, c)
		}
	}
}

// THE PHASES NEVER EXIT NON-ZERO — reaching a verdict is the score phase's
// job — so one that does is a broken script, and the message names WHICH.
func TestPythonMutationNamesThePhaseThatBroke(t *testing.T) {
	for _, phase := range mutationPhasesPython {
		engine.reset()
		engine.withTree(mutationTree("0\n", "every mutant killed\n"))
		engine.exitCode(`python.sh","`+phase+`"`, 1)
		engine.stdout(`python.sh","`+phase+`"`, "cosmic-ray: config not found")

		wantState(t, runAtom(t, "python:mutation", "abc123"), 2,
			"python:mutation: CANNOT RUN", "phase "+phase+" exited non-zero",
			"the phases never do on their own", "cosmic-ray: config not found")
	}

	// The engine's own failure is not the script's.
	engine.reset()
	engine.withTree(mutationTree("0\n", "every mutant killed\n"))
	engine.fail(`python.sh","init"`, "engine went away")
	wantState(t, runAtom(t, "python:mutation", "abc123"), 2, "never ran", "engine went away")
}

// The canonical script has ONE HOME, and a run that could not read it there is
// a gate that never looked.
func TestPythonMutationCannotRunWithoutTheCanonicalScript(t *testing.T) {
	engine.reset()
	engine.withTree(mutationTree("0\n", "every mutant killed\n"))
	engine.fail("ci/lib/mutation/python.sh", "no such file or directory")

	wantState(t, runAtom(t, "python:mutation", "abc123"), 2,
		"python:mutation: CANNOT RUN", "/stocks/ci/lib/mutation/python.sh is absent",
		"foundry-stocks did not mount at its one home")
	if n := len(engine.chains()); n != 1 {
		t.Errorf("a missing script must cost one contents read, not a container: %d queries", n)
	}
}
