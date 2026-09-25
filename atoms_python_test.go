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
	ruffToml = "/rulesets/ruff.toml"
	mypyIni  = "/rulesets/mypy.ini"
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
		[]string{"withMountedCache", `path:"/opt/uv-cache"`},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withNewFile", `path:"/rulesets/ruff.toml"`},
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

func TestPythonRuffFormatGradesAWheelBuilderAtEighty(t *testing.T) {
	// The same tree, plus a [build-system]: now the python is the SUBJECT of
	// the repository — a distribution it publishes — and 80 is the width.
	// chaos is this shape (pyproject + go.mod + hatchling, src/chaos), and a
	// rule that read only the manifest NAMES graded it at 100 and reddened 22
	// files that were already correct at the width its own ruff.toml declares.
	engine.reset()
	engine.withTree(pyTree(map[string]string{
		"pyproject.toml": "[project]\nname = \"x\"\n\n[build-system]\nrequires = [\"hatchling\"]\n",
	}))
	wantState(t, runAtom(t, "python:ruff-format", "base-sha"), 0)

	wantCalls(t, engine.chain(`"uvx","ruff@0.16.3","format"`, "exitCode"),
		[]string{"withExec", `expect:ANY`, `args:["uvx","ruff@0.16.3","format","--config","` + ruffToml + `","--line-length","80","--check","src","tests"]`},
	)
}

func TestPythonRuffFormatScopesAGoStarToItsProductPython(t *testing.T) {
	// A go star with product python: src and tests, in the shell loop's order.
	//
	// IT IS GRADED AT 100 BECAUSE everyLaneTree's pyproject.toml CARRIES NO
	// [build-system], and that is worth saying out loud rather than leaving
	// to be inferred from the argv. Reading this test as "a go star with
	// product python is 100" is how RuffLineLength got its second wrong rule:
	// this fixture's shape is chaos's, and chaos publishes a wheel and is
	// graded at 80. The width follows the build-system, not the sibling
	// manifest. The companion below pins the other direction.
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "python:ruff-format", "base-sha"), 0)

	c := engine.chain(`"uvx","ruff@0.16.3","format"`, "exitCode")
	wantCalls(t, c,
		[]string{"withNewFile", `path:"/rulesets/ruff.toml"`},
		[]string{"withExec", `args:["uvx","ruff@0.16.3","--version"]`},
		[]string{"withExec", `expect:ANY`, `args:["uvx","ruff@0.16.3","format","--config","` + ruffToml + `","--line-length","100","--check","src","tests"]`},
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
		[]string{"withNewFile", `path:"/rulesets/mypy.ini"`},
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
	wantState(t, v, 0, "python:mypy: ABSENT - no src/ or tests/ carrying python to type-check")
	if v.Result != "absent" {
		t.Errorf("want absent, got %q: %s", v.Result, v.Reason)
	}
	if c := engine.chain("withExec"); c != "" {
		t.Errorf("no target must cost no container:\n%s", c)
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
	for _, code := range []int{2, 3, 4} {
		engine.exitCode(`"pytest","-q"`, code)
		wantState(t, runAtom(t, "python:pytest", ""), 1)
	}

	// AN OOM KILL IS NOT IN THAT FOLD. 137 is in the signal range no Expect
	// covers, so the engine errors before PytestState sees a code: the suite
	// never ran, and the lane says so rather than filing a finding.
	engine.exitCode(`"pytest","-q"`, 137)
	wantState(t, runAtom(t, "python:pytest", ""), 2, "the atom never ran", "exit code: 137")

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
	wantState(t, v, 1, "python:pytest: FINDINGS - no test_*.py or *_test.py anywhere; nothing is built without tests")
	if v.Result == "absent" {
		t.Errorf("an empty python lane is red, never absent: %+v", v)
	}
	if c := engine.chain("withExec"); c != "" {
		t.Errorf("the emptiness is decided in Go and must cost no container:\n%s", c)
	}

	// The enumeration is a CANNOT RUN when the engine will not answer it.
	engine.reset()
	engine.withTree(pyTree(nil, "tests"))
	engine.fail(`glob(pattern:"**/*.py`, "filter: engine went away")
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

// python:mutation's needles: each exec the atom runs, by the words only it has.
const (
	pyBaseNeedle      = `"git","rev-parse","--verify","--quiet","abc123^{commit}"`
	pyNamesNeedle     = `"--name-only","--diff-filter=AM"`
	pyDiffNeedle      = `"git","diff","--unified=0","since0","HEAD"`
	pySyncNeedle      = `args:["uv","sync","--all-extras","--locked"]`
	pyInitNeedle      = `"cosmic-ray","init"`
	pyScopeNeedle     = `"forge-testkit-mutation","scope"`
	pyPluginNeedle    = `"forge-testkit-mutation","plugin"`
	pyPlanNeedle      = `"forge-testkit-mutation","plan"`
	pyMeasureNeedle   = `"coverage>=7.4"`
	pyMapNeedle       = `"forge-testkit-mutation","map"`
	pyPartitionNeedle = `"forge-testkit-mutation","partition"`
	pyExecNeedle      = `"cosmic-ray","exec"`
	pyMergeNeedle     = `"forge-testkit-mutation","merge"`
	pySelectionNeedle = `"forge-testkit-mutation","selection"`
	pyReportNeedle    = `"forge-testkit-mutation","report"`
)

// scriptPythonMutation answers a pull that added a line to src/x.py, a plan for
// two workers, and a clean report. No critical modules are declared unless
// tree hands the repo an answers file.
func scriptPythonMutation(tree map[string]string) {
	engine.reset()
	base := rustTSTree(map[string]string{"src/x.py": "x = 1\n"})
	delete(base, ".copier-answers.yml")
	engine.withTree(base)
	engine.withTree(tree)
	engine.stdout(mergeBaseNeedle, sinceSha+"\n")
	engine.stdout(pyNamesNeedle, "src/x.py\n")
	engine.stdout(pyDiffNeedle, "+++ b/src/x.py\n@@ -0,0 +1 @@\n+x = 1")
	engine.stdout(pyPlanNeedle, "pending=5\nworkers=2\n")
}

// python:mutation runs git, uv, cosmic-ray and forge-testkit-mutation as plain
// execs, measures and mutates on one branch per worker, and runs no script.
func TestPythonMutationMeasuresAndMutatesInPlainExecs(t *testing.T) {
	scriptPythonMutation(map[string]string{".copier-answers.yml": "critical_modules: src/x.py\n"})
	wantState(t, runAtom(t, "python:mutation", "abc123"), 0)

	// NO --index: the cache answers for the fleet's own distributions on the
	// intercepted pypi.org, so uv's default index reaches forge-testkit and
	// devpi is never asked about a package it does not have (infra#848).
	testkit := `"uv","run","--no-project","--isolated","--with","forge-testkit>=1.9.0","forge-testkit-mutation"`
	c := engine.chain(pyReportNeedle, "exitCode")
	if !strings.Contains(c, checks.ImagePython) {
		t.Errorf("python:mutation must run in the python lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"abc123"`},
		[]string{"withExec", `args:["uv","--version"]`},
		[]string{"withExec", `args:["prlimit","--version"]`},
		[]string{"withExec", "expect:ANY", pySyncNeedle},
		[]string{"withNewFile", `path:"cosmic-ray.toml"`, `module-path = \"src/x.py\"`, `timeout = 60.0`,
			`test-command = \"prlimit --data=4294967296 python -m pytest -x -q -p no:cacheprovider\"`},
		[]string{"withNewFile", `path:"/tmp/mutation/pr.diff"`, `+x = 1\n`},
		[]string{"withExec", "expect:ANY", `args:["uv","run","--with","cosmic-ray","cosmic-ray","init","cosmic-ray.toml","session.sqlite"]`},
		[]string{"withEnvVariable", `name:"GITHUB_OUTPUT"`, `value:"/tmp/mutation/scope.out"`},
		[]string{"withExec", "expect:ANY", `args:[` + testkit + `,"scope","session.sqlite","--diff","/tmp/mutation/pr.diff","--base","since0"]`},
		[]string{"withExec", "expect:ANY", `args:[` + testkit + `,"plugin","--out","/tmp/mutation/plugin"]`},
		[]string{"withEnvVariable", `name:"PYTEST_ADDOPTS"`, `value:"-p _forge_mutation_select"`},
		[]string{"withDirectory", `path:"/tmp/mutation/measure"`},
		[]string{"withExec", "expect:ANY", `args:[` + testkit + `,"map","--parts","/tmp/mutation/measure","--modules","src/x.py","--root","/src","--out","/tmp/mutation/map.json"]`},
		[]string{"withEnvVariable", `name:"FORGE_MUT_SELECT"`, `value:"/tmp/mutation/map.json"`},
		[]string{"withExec", "expect:ANY", `args:[` + testkit + `,"partition","session.sqlite","--map","/tmp/mutation/map.json","--out","/tmp/mutation/w1.sqlite","--out","/tmp/mutation/w2.sqlite"]`},
		[]string{"withFile", `path:"/tmp/mutation/parts/w2.sqlite"`},
		[]string{"withExec", "expect:ANY", `args:[` + testkit + `,"merge","session.sqlite","/tmp/mutation/parts/w1.sqlite","/tmp/mutation/parts/w2.sqlite"]`},
		[]string{"withExec", "expect:ANY", `args:[` + testkit + `,"report","session.sqlite","--fail-under","100"]`},
	)
	for _, probe := range []string{`args:["uv","--version"]`, `args:["prlimit","--version"]`} {
		if hasCall(c, "withExec", probe, "expect:ANY") {
			t.Errorf("%s is provisioning and must run under the default Expect:\n%s", probe, c)
		}
	}
	for _, relic := range []string{`path:"/stocks"`, `"bash"`, `name:"MUT_`, "ulimit"} {
		if strings.Contains(c, relic) {
			t.Errorf("the ported lane still carries %s:\n%s", relic, c)
		}
	}

	// worker answers the newest exit read of a chain with needle run as worker w.
	worker := func(needle, w string) string {
		for _, q := range reverse(engine.chains()) {
			if strings.Contains(q, needle) && strings.Contains(q, "exitCode") && hasCall(q, "withEnvVariable", `name:"FORGE_MUT_WORKER"`, `value:"`+w+`"`) {
				return q
			}
		}
		t.Errorf("no %s ran as worker %s", needle, w)
		return ""
	}
	// One measure and one exec per worker, told apart so the engine runs each.
	for _, w := range []string{"1", "2"} {
		m := worker(pyMeasureNeedle, w)
		wantCalls(t, m,
			[]string{"withEnvVariable", `name:"FORGE_MUT_WORKER"`, `value:"` + w + `"`},
			[]string{"withEnvVariable", `name:"FORGE_MUT_MEASURE"`, `value:"/tmp/mutation/measure"`},
			[]string{"withEnvVariable", `name:"FORGE_MUT_INCLUDE"`, `value:"src/x.py"`},
			[]string{"withEnvVariable", `name:"PYTHONDONTWRITEBYTECODE"`, `value:"1"`},
			[]string{"withExec", "expect:ANY", `args:["uv","run","--with","coverage>=7.4","prlimit","--data=4294967296","python","-m","pytest","-x","-q","-p","no:cacheprovider"]`},
		)
		e := worker(pyExecNeedle, w)
		wantCalls(t, e,
			[]string{"withFile", `path:"/src/session.sqlite"`},
			[]string{"withExec", "expect:ANY", `args:["uv","run","--with","cosmic-ray","cosmic-ray","exec","cosmic-ray.toml","session.sqlite"]`},
		)
	}

	// Survivors are the report's FAIL line, with the report.
	scriptPythonMutation(map[string]string{".copier-answers.yml": "critical_modules: src/x.py\n"})
	engine.exitCode(pyReportNeedle, 1)
	engine.stdout(pyReportNeedle, "src/x.py:1 survived: ReplaceBinaryOperator\n")
	engine.stderr(pyReportNeedle, "FAIL: honest score 50.0% is below the floor\n")
	wantState(t, runAtom(t, "python:mutation", "abc123"), 1,
		"scoped to the declared critical modules: src/x.py",
		"FAIL: honest score 50.0% is below the floor", "src/x.py:1 survived: ReplaceBinaryOperator")
}

// AN EMPTY DECLARATION IS THE WHOLE DIFF, NOT AN OPT-OUT, less the generated
// modules; and a scope that is all generated has nothing hand-written to mutate.
func TestPythonMutationScopesAnUndeclaredPullToItsHandWrittenPython(t *testing.T) {
	scriptPythonMutation(map[string]string{"src/gen.py": "# a header\n# GENERATED by protoc\nx = 1\n"})
	engine.stdout(pyNamesNeedle, "src/x.py\nsrc/gen.py\nsrc/pkg\n")
	wantState(t, runAtom(t, "python:mutation", "abc123"), 0)
	wantCalls(t, engine.chain(pyNamesNeedle, "stdout"),
		[]string{"withExec", "expect:ANY", `args:["git","diff","--name-only","--diff-filter=AM","since0","HEAD","--","*.py",":(exclude,glob)**/tests/**",":(exclude,glob)**/test_*.py",":(exclude,glob)**/*_test.py",":(exclude,glob)**/conftest.py"]`})
	// A directory, which does not read, is kept.
	wantCalls(t, engine.chain(pyDiffNeedle, "stdout"),
		[]string{"withExec", `args:["git","diff","--unified=0","since0","HEAD","--","src/x.py","src/pkg"]`})

	scriptPythonMutation(map[string]string{"src/gen.py": "# Code generated by x. DO NOT EDIT.\n"})
	engine.stdout(pyNamesNeedle, "src/gen.py\n")
	wantState(t, runAtom(t, "python:mutation", "abc123"), 0)
	if engine.chain(pyDiffNeedle) != "" {
		t.Errorf("an all-generated scope went on to diff")
	}
}

func TestPythonMutationStandsDownOrCannotRun(t *testing.T) {
	cases := map[string]struct {
		base   string
		script func()
		state  int
		// reason is checked only off a finding or could-not-run: a pass keeps no
		// output (checks.VerdictOf), so a stand-down is told apart by what ran.
		reason  []string
		reached []string
		never   []string
	}{
		"no base": {"", nil, 0, nil, nil, []string{pyBaseNeedle, `args:["uv","--version"]`}},
		"a base the history lacks": {"abc123", func() { engine.exitCode(pyBaseNeedle, 1) }, 0, nil,
			[]string{pyBaseNeedle}, []string{mergeBaseNeedle, pyNamesNeedle}},
		"no merge base": {"abc123", func() { engine.exitCode(mergeBaseNeedle, 1) }, 2,
			[]string{"no merge base between the base abc123 and HEAD (exit 1)"}, []string{mergeBaseNeedle}, []string{pyNamesNeedle}},
		"a merge base that answers nothing": {"abc123", func() { engine.stdout(mergeBaseNeedle, " \n") }, 2,
			[]string{"no merge base between the base abc123 and HEAD (exit 0)"}, nil, []string{pyNamesNeedle}},
		"no python changed": {"abc123", func() { engine.stdout(pyNamesNeedle, "") }, 0, nil,
			[]string{pyNamesNeedle}, []string{pyDiffNeedle}},
		"git cannot list the python": {"abc123", func() { engine.exitCode(pyNamesNeedle, 1) }, 2,
			[]string{"git could not list the python the pull changed"}, nil, []string{pyDiffNeedle}},
		"lines only removed": {"abc123", func() { engine.stdout(pyDiffNeedle, "+++ b/src/x.py\n@@ -1 +0,0 @@\n-x = 1") }, 0, nil,
			[]string{pyDiffNeedle}, []string{pySyncNeedle}},
		"git cannot diff": {"abc123", func() { engine.exitCode(pyDiffNeedle, 1) }, 2,
			[]string{"git could not diff the pull against its base since0"}, nil, []string{pySyncNeedle}},
		"the sync fails": {"abc123", func() {
			engine.exitCode(pySyncNeedle, 2)
			engine.stdout(pySyncNeedle, "error: the lockfile needs to be updated")
		}, 2, []string{"uv sync --all-extras --locked exited 2", "lockfile needs to be updated"}, nil, []string{pyInitNeedle}},
		"init enumerates nothing": {"abc123", func() { engine.exitCode(pyInitNeedle, 1) }, 2,
			[]string{"cosmic-ray init enumerated no mutation sites (exit 1)"}, nil, []string{pyScopeNeedle}},
		"the diff matches no site": {"abc123", func() {
			engine.exitCode(pyScopeNeedle, 2)
			engine.stdout(pyScopeNeedle, "no job's module is in the diff")
		}, 1, []string{"forge-testkit-mutation scope exited 2", "check critical-modules", "no job's module is in the diff"}, nil, []string{pyPluginNeedle}},
		"nothing mutable": {"abc123", func() { engine.withTree(map[string]string{"/tmp/mutation/scope.out": "kept=0\nunmutable=true\n"}) }, 0, nil,
			[]string{pyScopeNeedle}, []string{pyPluginNeedle}},
		"the plugin cannot be written": {"abc123", func() { engine.exitCode(pyPluginNeedle, 1) }, 2,
			[]string{"the test-selection plugin could not be written"}, nil, []string{pyPlanNeedle}},
		"the plan names no workers": {"abc123", func() { engine.stdout(pyPlanNeedle, "pending=0\n") }, 2,
			[]string{"the session could not be planned"}, nil, []string{pyMeasureNeedle}},
		"the plan fails": {"abc123", func() { engine.exitCode(pyPlanNeedle, 1) }, 2,
			[]string{"the session could not be planned (exit 1)"}, nil, []string{pyMeasureNeedle}},
		"the unmutated suite fails": {"abc123", func() {
			engine.exitCode(pyMeasureNeedle, 1)
			engine.stdout(pyMeasureNeedle, "collected 3 items\nFAILED tests/test_x.py::test_a\n1 failed")
		}, 2, []string{"the unmutated suite exited 1 in worker 1", "1 failed"}, nil, []string{pyMapNeedle}},
		"the unmutated suite runs out of memory": {"abc123", func() {
			engine.exitCode(pyMeasureNeedle, 1)
			engine.stdout(pyMeasureNeedle, "E   MemoryError")
		}, 2, []string{"ran out of memory under the 4G ceiling"}, nil, []string{pyMapNeedle}},
		"the map cannot be built": {"abc123", func() { engine.exitCode(pyMapNeedle, 1) }, 2,
			[]string{"the test map could not be built"}, nil, []string{pyPartitionNeedle}},
		"the session cannot be partitioned": {"abc123", func() { engine.exitCode(pyPartitionNeedle, 1) }, 2,
			[]string{"the session could not be partitioned across 2 workers"}, nil, []string{pyExecNeedle}},
		"a worker's exec breaks": {"abc123", func() { engine.exitCode(pyExecNeedle, 3) }, 2,
			[]string{"cosmic-ray exec exited 3"}, []string{pyMergeNeedle}, []string{pySelectionNeedle}},
		"a worker leaves jobs unscored": {"abc123", func() { engine.exitCode(pyMergeNeedle, 1) }, 2,
			[]string{"cosmic-ray exec exited 1"}, nil, []string{pySelectionNeedle}},
		"a mutant run measured nothing": {"abc123", func() { engine.exitCode(pySelectionNeedle, 3) }, 2,
			[]string{"matched none of its selected tests"}, nil, []string{pyReportNeedle}},
		"an unreadable selection summary": {"abc123", func() { engine.exitCode(pySelectionNeedle, 1) }, 0, nil,
			[]string{pyReportNeedle}, nil},
		"no mutants ran": {"abc123", func() {
			engine.exitCode(pyReportNeedle, 1)
			engine.stderr(pyReportNeedle, "no mutants ran")
		}, 2, []string{"no mutants ran — the scope kept sites"}, nil, nil},
		"an unreadable session": {"abc123", func() { engine.exitCode(pyReportNeedle, 2) }, 2,
			[]string{"forge-testkit-mutation report exited 2"}, nil, nil},
		"no prlimit in the image": {"abc123", func() { engine.fail(`args:["prlimit","--version"]`, "exec: prlimit: not found") }, 2,
			[]string{"never ran", "prlimit"}, nil, []string{pyNamesNeedle}},
		"the base check never ran":       {"abc123", func() { engine.fail(pyBaseNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{mergeBaseNeedle}},
		"the merge base never ran":       {"abc123", func() { engine.fail(mergeBaseNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{pyNamesNeedle}},
		"the names never ran":            {"abc123", func() { engine.fail(pyNamesNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{pyDiffNeedle}},
		"the diff never ran":             {"abc123", func() { engine.fail(pyDiffNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{pySyncNeedle}},
		"the sync never ran":             {"abc123", func() { engine.failLeaf(pySyncNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"init never ran":                 {"abc123", func() { engine.failLeaf(pyInitNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"scope never ran":                {"abc123", func() { engine.failLeaf(pyScopeNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"the plugin never ran":           {"abc123", func() { engine.failLeaf(pyPluginNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"the plan never ran":             {"abc123", func() { engine.failLeaf(pyPlanNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"a measure never ran":            {"abc123", func() { engine.failLeaf(pyMeasureNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"the map never ran":              {"abc123", func() { engine.failLeaf(pyMapNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"partition never ran":            {"abc123", func() { engine.failLeaf(pyPartitionNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"an exec never ran":              {"abc123", func() { engine.failLeaf(pyExecNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"the merge never ran":            {"abc123", func() { engine.failLeaf(pyMergeNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"selection never ran":            {"abc123", func() { engine.failLeaf(pySelectionNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"the report's exit never read":   {"abc123", func() { engine.failLeaf(pyReportNeedle, "exitCode", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"the report's stdout never read": {"abc123", func() { engine.failLeaf(pyReportNeedle, "stdout", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"the report's stderr never read": {"abc123", func() { engine.failLeaf(pyReportNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			scriptPythonMutation(nil)
			if c.script != nil {
				c.script()
			}
			wantState(t, runAtom(t, "python:mutation", c.base), c.state, c.reason...)
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

// AN AUDIT THAT COULD NOT REACH ITS ADVISORIES IS ASKED AGAIN, NOT FILED. The
// first ask times out against pypi.org and exits 1; read as a finding that
// would have been cached and replayed on every push of the tree (measured on
// iris, 2026-09-17). Read as could-not-run, verdictFor asks again past the
// cache, and the second answer — clean — is the verdict.
func TestPythonPipAuditNetworkFaultIsAskedAgainPastTheCache(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"pip-audit","pip-audit"`, 1)
	engine.stdout(`"pip-audit","pip-audit"`,
		"Installed 64 packages in 2.05s\nrequests.exceptions.ReadTimeout: HTTPSConnectionPool(host='pypi.org', port=443): Read timed out. (read timeout=15)\n")
	engine.exitCode(`name:"CA_REASK"`, 0)
	engine.stdout(`name:"CA_REASK"`, "No known vulnerabilities found\n")

	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "python:pip-audit")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != 0 {
		t.Errorf("a timeout on the first ask and a clean second ask is a pass: %+v", v)
	}
	if engine.chain(`"pip-audit","pip-audit"`, `name:"CA_REASK"`) == "" {
		t.Errorf("the audit was not asked again past the cache")
	}

	// The same exit 1 with a real table is a finding, and is never re-asked.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"pip-audit","pip-audit"`, 1)
	engine.stdout(`"pip-audit","pip-audit"`,
		"Found 1 known vulnerability in 1 package\nName    Version ID             Fix Versions\nurllib3 2.2.0   GHSA-34jh-p97f 2.2.2\n")
	v, err = verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "python:pip-audit")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != 1 || !strings.Contains(v.Reason, "GHSA-34jh-p97f") {
		t.Errorf("a real finding is a finding, carrying the table: %+v", v)
	}
	if engine.chain(`name:"CA_REASK"`) != "" {
		t.Errorf("a finding was asked again")
	}
}

// ---- a lane declared by its files has to be able to RUN (2026-09-25) ----

// `uv run` RESOLVES A PROJECT OR IT FAILS. Widening the lane to any tree
// carrying a .py is nominal unless the tools can run in a tree that is not a
// python project — foundry-stocks' 234 hook tests live in hooks/, declared by
// no manifest, and furnace renders those hooks into every session's live
// .claude/hooks/. This is the pair of assertions that makes the widening real.
func TestPythonToolsRunInATreeThatIsNotAProject(t *testing.T) {
	// pytest, with no pyproject.toml anywhere.
	engine.reset()
	engine.withTree(pyTree(map[string]string{"hooks/test_hook.py": ""}, "pyproject.toml", "src", "tests"))
	wantState(t, runAtom(t, "python:pytest", ""), 0)
	c := engine.chain(`"pytest","-q"`, "exitCode")
	if !hasCall(c, "withExec", `"--no-project","--with","pytest","pytest","-q"]`) {
		t.Errorf("no project means --no-project and the tool fetched with --with:\n%s", c)
	}
	if strings.Contains(c, "--all-extras") {
		t.Errorf("--all-extras is a project flag and there is no project:\n%s", c)
	}

	// AND THE PROJECT FORM IS UNTOUCHED. A repo that IS a python project still
	// runs its checks against its own declared extras, which is the whole
	// reason --all-extras was there.
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "python:pytest", ""), 0)
	if c := engine.chain(`"pytest","-q"`, "exitCode"); !hasCall(c, "withExec", `"uv","run","--all-extras","pytest","-q"]`) {
		t.Errorf("a project's checks run against its own extras:\n%s", c)
	}
}

// A GO STAR'S src/ IS NOT PYTHON, and mypy pointed at one answers exit 2 —
// this module's word for "the check did not happen". urania, helios and nyx
// each carry src/ and tests/ full of .go beside an incidental .py, and before
// the lane was declared by its files they were simply not in it.
func TestPythonMypySkipsProductDirsThatHoldNoPython(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{
		"go.mod":          "module x",
		"src/main.go":     "",
		"tests/x_test.go": "",
		"scripts/one.py":  "",
	})
	v := runAtom(t, "python:mypy", "")
	wantState(t, v, 0, "python:mypy: ABSENT - no src/ or tests/ carrying python to type-check")
	if v.Result != "absent" {
		t.Errorf("want absent, got %q: %s", v.Result, v.Reason)
	}
	if c := engine.chain("withExec"); c != "" {
		t.Errorf("mypy must never be provisioned to type-check a Go tree:\n%s", c)
	}
}

// THE `Lanes` VERB HAS TO SAY WHICH HALF IS DECLARED, because the files and
// the manifest now answer different questions and a session asking what runs
// here wants both. A repo with .py and no pyproject lints and tests and does
// not build, and that is a sentence, not an omission.
func TestManifestStateNamesWhatIsMissing(t *testing.T) {
	withIt := manifestState([]string{"pyproject.toml"}, checks.LanePython)
	if withIt != "pyproject.toml" {
		t.Errorf("a declared manifest names itself, got %q", withIt)
	}
	without := manifestState([]string{"README.md"}, checks.LanePython)
	if !strings.Contains(without, "no pyproject.toml") {
		t.Errorf("an absent manifest is named, got %q", without)
	}
	if !strings.Contains(without, "lint and test, no build") {
		t.Errorf("and the consequence is said out loud, got %q", without)
	}
}

// The enumeration is a CANNOT RUN for mypy the same way it is for pytest: a
// tree the engine will not list is a fact about the repository, and mypy
// cannot choose its targets without it.
func TestPythonMypyCannotRunWhenTheTreeWillNotList(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`glob(pattern:"**/*.py`, "filter: engine went away")
	wantState(t, runAtom(t, "python:mypy", ""), 2,
		"python:mypy: CANNOT RUN", "could not enumerate the repository", "engine went away")
}

// MYPY DOES NOT RUN IN A TREE THAT IS NOT A PROJECT, and this is asserted
// through the PLANNER because that is the only place it is decided — runAtom
// calls a runner directly, so an assertion made there would have passed
// whatever the plan said, which is exactly how the first cut of this shipped.
//
// MEASURED on foundry-dies the day the lane widened: a repo whose only python
// is a pytest test file reported `Cannot find implementation or library stub
// for module named "pytest"`, and the same command with the framework present
// reported no issues. The repository was clean; the checker could not see it.
func TestPythonMypyStandsDownWithoutAManifestWhileLintStillRuns(t *testing.T) {
	tree := map[string]string{
		"README.md":               "",
		"tests/test_contracts.py": "",
	}
	for _, want := range []struct {
		id     string
		absent bool
	}{
		{"python:mypy", true},
		{"python:ruff-check", false},
		{"python:pytest", false},
	} {
		engine.reset()
		engine.withTree(tree)
		vs, err := (&FoundryTools{Source: dag.Directory()}).vector(t.Context(), checks.AtomByID(want.id).Stage, want.id, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(vs) != 1 {
			t.Fatalf("%s: one atom asked, %d answered", want.id, len(vs))
		}
		v := vs[0]
		if want.absent {
			if v.Result != "absent" || v.State != 0 {
				t.Errorf("%s: want an absent 0, got %+v", want.id, v)
			}
			if !strings.Contains(v.Reason, "no pyproject.toml at the repository root") {
				t.Errorf("%s: the absence names the manifest it wanted: %s", want.id, v.Reason)
			}
			// AND IT SAYS THE LANE IS STILL RUNNING. "does not build the lane"
			// would be a lie here: ruff and pytest are running on this very
			// tree, and a reader who believed the lane was off would go
			// looking for the wrong thing.
			if !strings.Contains(v.Reason, "lint and tests still run") {
				t.Errorf("%s: the absence must not read as the lane being off: %s", want.id, v.Reason)
			}
			continue
		}
		if v.Result == "absent" {
			t.Errorf("%s: a .py-declared lane still lints and tests, got %+v", want.id, v)
		}
	}
}
