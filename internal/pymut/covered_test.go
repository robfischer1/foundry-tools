package pymut

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// testOnly is a pull that changed only tests/test_x.py, whose run covers
// src/x.py:1 (the report the fake `coverage json` writes). The test file's own
// lines and conftest's are in the report too, as coverage writes them.
func testOnly(t *testing.T, answers string) *pull {
	t.Helper()
	p := newPull(t, answers)
	putFile(t, p.in.Root, "tests/test_x.py", "def test_x():\n    assert True\n")
	putFile(t, p.in.Root, "src/y.py", "y = 1\ny = 2\n")
	p.first(rule{"--name-only --diff-filter=AM", "", 0, ""})
	p.first(rule{":(glob)**/test_*.py", "tests/test_x.py\n", 0, ""})
	p.first(rule{"diff -M --unified=0", "diff --git a/tests/test_x.py b/tests/test_x.py\n+++ b/tests/test_x.py\n@@ -0,0 +2 @@\n+    assert True", 0, ""})
	p.covJSON = `{"files":{"src/x.py":{"executed_lines":[1]},"tests/test_x.py":{"executed_lines":[1,2]},"conftest.py":{"executed_lines":[1]},` +
		`"` + filepath.Join(p.work, "python", "src", "src/y.py") + `":{"executed_lines":[1,2]},".venv/lib/site.py":{"executed_lines":[3]}}}`
	return p
}

// A test-only change is mutation-tested by the source its tests run: the changed
// test runs alone under coverage, the source lines it ran join the scope diff,
// and the test file itself is never mutated.
func TestCoveredATestOnlyPythonChangeMutatesTheSourceItsTestsRun(t *testing.T) {
	p := testOnly(t, "")
	v := p.run()
	if strings.Contains(said(v), "nothing to mutate") || strings.Contains(said(v), "no hand-written python") {
		t.Fatalf("a test-only pull took the empty-change-set exit: %s", said(v))
	}
	run, ok := p.call("coverage run")
	if !ok || !slices.Contains(run.Args, "tests/test_x.py") || !slices.Contains(run.Args, "--include") || !slices.Contains(run.Args, "pytest") {
		t.Fatalf("the changed test was not run alone under coverage: %v\n%s", run.Args, strings.Join(p.runs, "\n"))
	}
	raw, _ := os.ReadFile(filepath.Join(p.work, "python", "pr.diff"))
	diff := string(raw)
	if !strings.Contains(diff, "+++ b/src/x.py\n@@ -0,0 +1,1 @@\n+x = 1\n") || !strings.Contains(diff, "+++ b/src/y.py\n@@ -0,0 +1,2 @@\n") {
		t.Fatalf("the covered source is not in the scope diff: %q", diff)
	}
	for _, never := range []string{"test_x.py", "conftest.py", ".venv"} {
		if strings.Contains(diff, never) {
			t.Errorf("%s is in the scope diff: %q", never, diff)
		}
	}
	if !strings.Contains(said(v), "python:mutation: changed tests: 3 source line(s) in 2 file(s) they run joined the scope") {
		t.Errorf("the verdict does not say what the tests added: %s", said(v))
	}
	// The testkit's --base drops an added line inside an AST-unchanged unit,
	// which every line a test only RUNS is: the scope is the diff alone here.
	if scope, _ := p.call("forge-testkit-mutation scope"); slices.Contains(scope.Args, "--base") {
		t.Errorf("the scope asked with --base, which drops every covered line: %v", scope.Args)
	}
	if n := p.count("uv sync --all-extras --locked"); n != 3 {
		t.Errorf("the primary is synced once for the coverage run and the measure: %d syncs", n)
	}
}

// The bound applies: whole files past ATOM_MUTATION_COVERED_CAP are cut and the
// verdict says so; a cut is never a failure.
func TestCoveredPythonLinesAreBoundedByTheCap(t *testing.T) {
	p := testOnly(t, "")
	t.Setenv(checks.EnvMutationCoveredCap, "1")
	v := p.run()
	raw, _ := os.ReadFile(filepath.Join(p.work, "python", "pr.diff"))
	if !strings.Contains(said(v), "2 line(s) in 1 file(s) cut at the "+checks.EnvMutationCoveredCap+" bound (src/y.py)") || strings.Contains(string(raw), "src/y.py") {
		t.Fatalf("pr.diff %q: %s", raw, said(v))
	}
}

// Declared critical modules bound the covered lines too.
func TestCoveredPythonLinesStayInsideTheDeclaredModules(t *testing.T) {
	p := testOnly(t, "critical_modules: src/x.py\n")
	v := p.run()
	run, _ := p.call("coverage run")
	if !slices.Contains(run.Args, filepath.Join(p.work, "python", "src", "src/x.py")) {
		t.Errorf("--include is not the declared module: %v", run.Args)
	}
	raw, _ := os.ReadFile(filepath.Join(p.work, "python", "pr.diff"))
	if !strings.Contains(string(raw), "+++ b/src/x.py") || strings.Contains(string(raw), "src/y.py") {
		t.Fatalf("pr.diff %q: %s", raw, said(v))
	}
}

// Tests that wrote no coverage add nothing, and the pull stands down as it did.
func TestCoveredPythonTestsThatWroteNoCoverageAddNothing(t *testing.T) {
	p := testOnly(t, "")
	p.covJSON = ""
	v := p.run()
	if v.State != 0 || !strings.Contains(said(v), "no hand-written python in scope") || !strings.Contains(said(v), "wrote no coverage") {
		t.Fatalf("%s", said(v))
	}
}

// The edges: a changed helper runs the tests beside it; a generated module the
// tests run is not mutated; a listing git refuses, a report that does not parse
// and a directory that cannot be made are said, not failed; an environment that
// cannot sync is the run's could-not-run.
func TestCoveredPythonEdges(t *testing.T) {
	p := testOnly(t, "")
	p.first(rule{":(glob)**/test_*.py", "tests/helpers.py\nsrc/x.py\n", 0, ""})
	putFile(t, p.in.Root, "src/y.py", "# GENERATED by protoc\ny = 2\n")
	v := p.run()
	run, _ := p.call("coverage run")
	raw, _ := os.ReadFile(filepath.Join(p.work, "python", "pr.diff"))
	if !slices.Contains(run.Args, "tests") || strings.Contains(string(raw), "src/y.py") {
		t.Errorf("helper %v, pr.diff %q: %s", run.Args, raw, said(v))
	}

	p = testOnly(t, "")
	p.first(rule{":(glob)**/test_*.py", "fatal: bad\n", 128, ""})
	if v := p.run(); !strings.Contains(said(v), "git could not list them") || p.ran("coverage run") {
		t.Errorf("%s", said(v))
	}

	p = testOnly(t, "")
	p.covJSON = "not json"
	if v := p.run(); !strings.Contains(said(v), "the coverage report did not parse") {
		t.Errorf("%s", said(v))
	}

	p = testOnly(t, "")
	p.failSyncFrom = 1
	if v := p.run(); v.State != 2 || !strings.Contains(said(v), "uv sync") || p.ran("coverage run") {
		t.Errorf("%s", said(v))
	}

	p = testOnly(t, "")
	mkdirAll = func(dir string, m os.FileMode) error {
		if strings.HasSuffix(dir, "testcov") {
			return os.ErrPermission
		}
		return os.MkdirAll(dir, m)
	}
	t.Cleanup(func() { mkdirAll = os.MkdirAll })
	if v := p.run(); !strings.Contains(said(v), "changed tests: permission denied") {
		t.Errorf("%s", said(v))
	}
}

// A pull that changed no test runs no coverage.
func TestCoveredAPullWithNoTestChangeRunsNoCoverage(t *testing.T) {
	p := newPull(t, "")
	if v := p.run(); v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	if p.ran("coverage run") {
		t.Fatalf("a pull that changed no test ran coverage:\n%s", strings.Join(p.runs, "\n"))
	}
	if scope, _ := p.call("forge-testkit-mutation scope"); !slices.Contains(scope.Args, "--base") {
		t.Errorf("a pull with only its own diff lost the reformat filter: %v", scope.Args)
	}
}
