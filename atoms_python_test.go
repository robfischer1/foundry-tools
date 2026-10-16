package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE PYTHON LANE'S CHAIN TESTS, against the paper engine (engine_fake_test.go):
// python:mutation, the lane's provisioning and the planner's reading of its
// manifest. The lane's other atoms live in atoms/python/<name>/ and are tested
// beside their files.

// ---- pip-audit ----

// pipAuditNeedle finds the audit exec in a chain: the pinned requirement, then the program.
const pipAuditNeedle = `"pip-audit==` + checks.PipAuditVersion + `","pip-audit"`

// ---- mutation ----

// python:mutation's needles: each exec the atom runs, by the words only it has.
const (
	pyBaseNeedle      = `"git","rev-parse","--verify","--quiet","abc123^{commit}"`
	pyNamesNeedle     = `"--name-only","--diff-filter=AM"`
	pyDiffNeedle      = `"git","diff","-M","--unified=0","since0","HEAD"`
	pySyncNeedle      = `args:["uv","sync","--all-extras","--locked"]`
	pyInitNeedle      = `"cosmic-ray","init"`
	pyScopeNeedle     = `"forge-testkit-mutation","scope"`
	pyPluginNeedle    = `"forge-testkit-mutation","plugin"`
	pyPlanNeedle      = `"forge-testkit-mutation","plan"`
	pyMeasureNeedle   = `"coverage==` + checks.CoverageVersion + `"`
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
	engine.stdout(pyDiffNeedle, "diff --git a/src/x.py b/src/x.py\n+++ b/src/x.py\n@@ -0,0 +1 @@\n+x = 1")
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
	testkit := `"uv","run","--no-project","--isolated","--with","forge-testkit>=2.1.0","forge-testkit-mutation"`
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
		[]string{"withExec", "expect:ANY", `args:["uv","run","cosmic-ray","init","cosmic-ray.toml","session.sqlite"]`},
		[]string{"withEnvVariable", `name:"GITHUB_OUTPUT"`, `value:"/tmp/mutation/scope.out"`},
		[]string{"withExec", "expect:ANY", `args:[` + testkit + `,"scope","session.sqlite","--diff","/tmp/mutation/pr.diff","--base","since0",` + pythonExcludes() + `]`},
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
			[]string{"withExec", "expect:ANY", `args:["uv","run","--with","coverage==` + checks.CoverageVersion + `","prlimit","--data=4294967296","python","-m","pytest","-x","-q","-p","no:cacheprovider"]`},
		)
		e := worker(pyExecNeedle, w)
		wantCalls(t, e,
			[]string{"withFile", `path:"/src/session.sqlite"`},
			[]string{"withExec", "expect:ANY", `args:["uv","run","cosmic-ray","exec","cosmic-ray.toml","session.sqlite"]`},
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
	// The whole tree is diffed with renames paired, and narrowed to the kept
	// modules in Go (checks.PythonDiffFor): a pathspec cannot see an old path.
	wantCalls(t, engine.chain(pyDiffNeedle, "stdout"),
		[]string{"withExec", `args:["git","diff","-M","--unified=0","since0","HEAD"]`})

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
		"lines only removed": {"abc123", func() {
			engine.stdout(pyDiffNeedle, "diff --git a/src/x.py b/src/x.py\n+++ b/src/x.py\n@@ -1 +0,0 @@\n-x = 1")
		}, 0, nil,
			[]string{pyDiffNeedle}, []string{pySyncNeedle}},
		"a module moved whole": {"abc123", func() {
			engine.stdout(pyDiffNeedle, "diff --git a/old/x.py b/src/x.py\nsimilarity index 100%\nrename from old/x.py\nrename to src/x.py")
		}, 0, nil, []string{pyDiffNeedle}, []string{pySyncNeedle}},
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
	engine.exitCode(pipAuditNeedle, 1)
	engine.stdout(pipAuditNeedle,
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
	if engine.chain(pipAuditNeedle, `name:"CA_REASK"`) == "" {
		t.Errorf("the audit was not asked again past the cache")
	}

	// The same exit 1 with a real table is a finding, and is never re-asked.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(pipAuditNeedle, 1)
	engine.stdout(pipAuditNeedle,
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

// pythonExcludes is the --exclude-operator pairs the Python rows put on the
// scope argv, as the engine renders them.
func pythonExcludes() string {
	var parts []string
	for _, op := range checks.SkippedMutators(checks.MutatorPython) {
		parts = append(parts, `"--exclude-operator",`+strconv.Quote(op))
	}
	return strings.Join(parts, ",")
}

// THE PYTHON LANE'S TOOLS ARE INSTALLED ONCE, AT EXACT VERSIONS, IN THE
// PROVISION LAYER. `uvx ruff@x` and `uv run --with cosmic-ray` resolved the tool
// on every run; the go lane's `go install <module>@<version>` is the shape this
// holds them to. Asserted on the chain: the install and its probe are there, are
// exact, and sit BEFORE the cache volumes mount (a layer never reads a volume).
func TestPythonLaneInstallsItsToolsOnceAtExactVersions(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "python:pip-audit", ""), 0)
	c := engine.chain(`"pip-audit"`, "exitCode")

	cacheAt := strings.Index(c, `path:"/opt/uv-cache"`)
	if cacheAt < 0 {
		t.Fatalf("the uv cache volume is not mounted:\n%s", c)
	}
	for _, tool := range checks.PythonLaneTools {
		install := `args:["uv","tool","install","` + tool.Name + `==` + tool.Version + `"]`
		at := strings.Index(c, install)
		if at < 0 {
			t.Errorf("the lane must install %s at its exact pin (%s):\n%s", tool.Name, install, c)
			continue
		}
		if at > cacheAt {
			t.Errorf("%s is installed after the cache volume mounts; a provision layer must not read a volume", tool.Name)
		}
		if !hasCall(c, "withExec", `args:["`+strings.Join(tool.Probe, `","`)+`"]`) {
			t.Errorf("%s has no probe %v", tool.Name, tool.Probe)
		}
	}
	for _, env := range []string{`name:"UV_TOOL_DIR"`, `name:"UV_TOOL_BIN_DIR"`} {
		if !strings.Contains(c, env) {
			t.Errorf("the lane must set %s for `uv tool install`:\n%s", env, c)
		}
	}
}

// NO `uv run --with` IN THE PYTHON LANE NAMES A FLOATING VERSION. The one floor
// that remains is forge-testkit's, which comes from the fleet's own index and is
// a floor on purpose (checks.PythonMutationTestkit).
func TestPythonLaneNamesNoFloatingWith(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "python:pip-audit", ""), 0)
	all := strings.Join(engine.chains(), "\n")
	for _, m := range regexp.MustCompile(`"--with","([^"]+)"`).FindAllStringSubmatch(all, -1) {
		if !strings.Contains(m[1], "==") {
			t.Errorf("`--with %s` floats; pin it with == in checks/toolpins.go", m[1])
		}
	}
}
