package pymut

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/unitkey"
)

// THE SOURCE A CHANGED TEST RUNS joins the scope (checks/testcovered.go says
// why). The tests the change touched, by pytest's conventions, run alone under
// coverage in the synced primary copy; every source line they executed inside
// the source package is graded as though it had changed. Test files are never
// mutated. A change with no test file in it runs nothing here.

// changedTestSpecs is the pathspec of every python file pytest's conventions
// call a test. `:(glob)` with `**/` matches at the root too.
var changedTestSpecs = []string{":(glob)**/test_*.py", ":(glob)**/*_test.py", ":(glob)**/tests/**"}

// testCovered answers the source lines the change's tests execute, named from
// the root, and the verdict's line for them; nil and "" when no test changed. A
// tree that cannot be synced is the atom's could-not-run, as the measure's would
// be: there is work, and nothing of it could be run.
func (g *grading) testCovered(ctx context.Context, since string, declared []string) (map[string][]unitkey.Range, string, *checks.Verdict) {
	s := g.do(ctx, g.in.Root, nil, "git", append([]string{"diff", "--diff-filter=AM", "--name-only", since, "HEAD", "--"}, changedTestSpecs...)...)
	if s.code != 0 {
		return nil, id + ": changed tests: git could not list them, so their coverage joined no scope: " + lastLines(s.out, 1), nil
	}
	var targets []string
	for _, f := range strings.Fields(s.out) {
		t := f
		if !checks.PythonTestFile(f) {
			continue
		}
		if !checks.PythonTestModule(f) {
			t = path.Dir(f) // a helper: the tests beside it
		}
		if !slices.Contains(targets, t) {
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		return nil, "", nil
	}
	if v, ok := g.prepare(ctx); !ok {
		return nil, "", &v
	}
	report, why := g.coverageOf(ctx, targets, declared)
	if why != "" {
		return nil, why, nil
	}
	covered, err := checks.PythonCoveredRanges(report, g.tree)
	if err != nil {
		return nil, id + ": changed tests: " + err.Error() + ", so they added nothing", nil
	}
	covered = checks.KeepCovered(covered, func(f string) bool {
		if checks.PythonTestFile(f) || path.Base(f) == "conftest.py" || strings.HasPrefix(f, ".venv/") || !strings.HasSuffix(f, ".py") {
			return false
		}
		if len(declared) > 0 && !checks.UnderAny(f, declared) {
			return false
		}
		src, err := readFile(filepath.Join(g.in.Root, f))
		return err == nil && !checks.PythonGenerated(string(src))
	})
	kept, say := checks.CoveredScope(id, "they run", covered)
	return kept, say, nil
}

// coverageOf runs the targets under coverage and answers its json report, or why
// there is none. --include is the declared modules, else the tree less its
// environment: a test's reach into a library is not this pull's to mutate.
func (g *grading) coverageOf(ctx context.Context, targets, declared []string) (string, string) {
	dir := filepath.Join(g.work, "testcov")
	if err := mkdirAll(dir, 0o755); err != nil {
		return "", id + ": changed tests: " + err.Error()
	}
	data, out := filepath.Join(dir, ".coverage"), filepath.Join(dir, "coverage.json")
	include := []string{filepath.Join(g.tree, "*")}
	if len(declared) > 0 {
		include = nil
		for _, m := range declared {
			p := filepath.Join(g.tree, m)
			if !strings.HasSuffix(m, ".py") {
				p = filepath.Join(p, "*")
			}
			include = append(include, p)
		}
	}
	with := []string{"run", "--with", checks.PythonWith("coverage")}
	// The suite's own memory ceiling (PythonTestCommand's prlimit), one process:
	// xdist's workers are other processes coverage would not follow.
	args := append(slicesClone(with), checks.PythonTestCommand[:2]...)
	args = append(args, "python", "-m", "coverage", "run", "--data-file", data, "--include", strings.Join(include, ","),
		"--omit", filepath.Join(g.tree, ".venv", "*"), "-m", "pytest", "-q", "-p", "no:cacheprovider")
	ran := g.do(ctx, g.tree, []string{"PYTEST_XDIST_AUTO_NUM_WORKERS=0"}, "uv", append(args, targets...)...)
	reported := g.do(ctx, g.tree, nil, "uv", append(slicesClone(with), "python", "-m", "coverage", "json", "--data-file", data, "-o", out)...)
	raw, err := readFile(out)
	if err != nil {
		return "", fmt.Sprintf("%s: changed tests wrote no coverage (pytest exit %d, coverage json exit %d), so they added nothing: %s", id, ran.code, reported.code, lastLines(ran.out, 1))
	}
	return string(raw), ""
}
