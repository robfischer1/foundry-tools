package pymut

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

const testkit = "run --no-project --isolated --refresh-package forge-testkit --with forge-testkit>=2.1.0 forge-testkit-mutation"

// python:mutation runs git, uv, cosmic-ray and forge-testkit-mutation as plain
// execs, measures and mutates on one copy of the tree per worker, and runs no
// script.
func TestPythonMutationMeasuresAndMutatesInPlainExecs(t *testing.T) {
	p := newPull(t, "critical_modules: src/x.py\n")
	if v := p.run(); v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	tree := filepath.Join(p.work, "python", "src")
	diffFile := filepath.Join(p.work, "python", "pr.diff")
	plugin := filepath.Join(p.work, "python", "plugin")
	measure := filepath.Join(p.work, "python", "measure")
	mapFile := filepath.Join(p.work, "python", "map.json")
	w1, w2 := filepath.Join(p.work, "python", "w1"), filepath.Join(p.work, "python", "w2")

	// NO --index: the cache answers for the fleet's own distributions on the
	// intercepted pypi.org, so uv's default index reaches forge-testkit and devpi
	// is never asked about a package it does not have (infra#848).
	want := []string{
		"uv --version", "prlimit --version",
		"git rev-parse --verify --quiet abc123^{commit}", "git merge-base abc123 HEAD",
		"git diff -M --unified=0 since0 HEAD",
		"uv sync --all-extras --locked", // the primary
		"uv run cosmic-ray init cosmic-ray.toml session.sqlite",
		"uv " + testkit + " scope session.sqlite --diff " + diffFile + " --base since0 " + strings.Join(excludes(), " "),
		"uv " + testkit + " plugin --out " + plugin,
		"uv " + testkit + " plan session.sqlite --workers 4",
		"uv " + testkit + " map --parts " + measure + " --modules src/x.py --root " + tree + " --out " + mapFile,
		"uv " + testkit + " partition session.sqlite --map " + mapFile + " --out " + filepath.Join(p.work, "python", "w1.sqlite") + " --out " + filepath.Join(p.work, "python", "w2.sqlite"),
		"uv " + testkit + " merge session.sqlite " + filepath.Join(p.work, "python", "parts", "w1.sqlite") + " " + filepath.Join(p.work, "python", "parts", "w2.sqlite"),
		"uv " + testkit + " selection " + filepath.Join(p.work, "python", "select.log"),
		"uv " + testkit + " report session.sqlite --fail-under 100",
	}
	for _, w := range want {
		if !p.ran(w) {
			t.Errorf("never ran %q:\n%s", w, strings.Join(p.runs, "\n"))
		}
	}
	if n := p.count("uv sync --all-extras --locked"); n != 3 {
		t.Errorf("the primary and each of the two workers sync their own environment: %d syncs", n)
	}
	// the project's own environment is never carried into a copy: an editable
	// install names its tree absolutely, so a copied .venv would import the original
	for _, w := range []string{tree, w1, w2} {
		if _, err := os.Stat(filepath.Join(w, ".venv")); err == nil {
			t.Errorf("%s carries the consumer's .venv", w)
		}
		if _, err := os.Stat(filepath.Join(w, ".git")); err == nil {
			t.Errorf("%s carries a .git", w)
		}
		if _, err := os.Stat(filepath.Join(w, "src/x.py")); err != nil {
			t.Errorf("%s is not a copy of the tree: %v", w, err)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(tree, "cosmic-ray.toml")); !strings.Contains(string(raw), `module-path = "src/x.py"`) ||
		!strings.Contains(string(raw), `timeout = 60.0`) ||
		!strings.Contains(string(raw), `test-command = "prlimit --data=4294967296 python -m pytest -x -q -p no:cacheprovider"`) {
		t.Errorf("cosmic-ray.toml:\n%s", raw)
	}
	if raw, _ := os.ReadFile(diffFile); string(raw) != "diff --git a/src/x.py b/src/x.py\n+++ b/src/x.py\n@@ -0,0 +1 @@\n+x = 1\n\n" {
		t.Errorf("pr.diff = %q", raw)
	}
	// the workers' config carries the timeout derived from the measured suite
	// (this one measured in under a second, so the floor)
	config := checks.CosmicRayConfig([]string{"src/x.py"}, checks.PythonTestCommand, checks.PythonMutantTimeout(0))
	for _, w := range []string{tree, w1, w2} {
		if raw, _ := os.ReadFile(filepath.Join(w, "cosmic-ray.toml")); string(raw) != config {
			t.Errorf("%s's config:\n%s\nwant\n%s", w, raw, config)
		}
	}
	// One measure and one exec per worker, told apart so each runs.
	for i, w := range []string{w1, w2} {
		num := string(rune('1' + i))
		m, ok := p.call("uv run --with coverage==" + checks.CoverageVersion + " prlimit")
		if !ok {
			t.Fatalf("no measure ran")
		}
		_ = m
		var measured, executed bool
		for j, cmd := range p.cmds {
			env := strings.Join(cmd.Env, "\n")
			if cmd.Dir == w && strings.Contains(p.runs[j], "uv run --with coverage") {
				measured = strings.Contains(env, "FORGE_MUT_WORKER="+num) && strings.Contains(env, "FORGE_MUT_MEASURE="+measure) &&
					strings.Contains(env, "FORGE_MUT_INCLUDE=src/x.py") && strings.Contains(env, "PYTHONDONTWRITEBYTECODE=1") &&
					strings.Contains(env, "PYTHONPATH="+plugin) && strings.Contains(env, "PYTEST_ADDOPTS=-p _forge_mutation_select") &&
					strings.HasSuffix(p.runs[j], strings.Join(checks.PythonTestCommand, " "))
			}
			if cmd.Dir == w && strings.Contains(p.runs[j], "cosmic-ray exec") {
				executed = strings.Contains(env, "FORGE_MUT_WORKER="+num) && strings.Contains(env, "FORGE_MUT_SELECT="+mapFile) &&
					strings.Contains(env, "FORGE_MUT_SELECT_LOG="+filepath.Join(p.work, "python", "select.log")+"."+num) &&
					strings.HasSuffix(p.runs[j], "cosmic-ray exec cosmic-ray.toml session.sqlite")
			}
		}
		if !measured || !executed {
			t.Errorf("worker %s: measured %v executed %v:\n%s", num, measured, executed, strings.Join(p.runs, "\n"))
		}
		if raw, _ := os.ReadFile(filepath.Join(w, "session.sqlite")); !strings.Contains(string(raw), "w"+num+".sqlite") {
			t.Errorf("worker %s did not get its share of the session: %q", num, raw)
		}
	}
	// the workers' selection logs are joined, in worker order, for the summary
	if raw, _ := os.ReadFile(filepath.Join(p.work, "python", "select.log")); string(raw) != "selected by w1\nselected by w2\n" {
		t.Errorf("select.log = %q", raw)
	}
	// the probes are provisioning: a missing program is the atom's could-not-run
	for _, r := range p.runs {
		for _, relic := range []string{"sh -c", "bash", "MUT_", "ulimit"} {
			if strings.Contains(r, relic) {
				t.Errorf("the atom ran %q", r)
			}
		}
	}
	// git is asked of the original, whose history the copies do not carry
	if d, _ := p.call("git diff -M"); d.Dir != p.in.Root {
		t.Errorf("git ran in %s", d.Dir)
	}
}

// Survivors are the report's FAIL line, with the report.
func TestPythonMutationReportsSurvivors(t *testing.T) {
	p := newPull(t, "critical_modules: src/x.py\n")
	p.first(rule{"forge-testkit-mutation report", "src/x.py:1 survived: ReplaceBinaryOperator\n", 1, "FAIL: honest score 50.0% is below the floor\n"})
	v := p.run()
	if v.State != 1 {
		t.Fatalf("%s", said(v))
	}
	for _, w := range []string{"scoped to the declared critical modules: src/x.py", "FAIL: honest score 50.0% is below the floor", "src/x.py:1 survived: ReplaceBinaryOperator"} {
		if !strings.Contains(said(v), w) {
			t.Errorf("the verdict lacks %q:\n%s", w, said(v))
		}
	}
}

// AN EMPTY DECLARATION IS THE WHOLE DIFF, NOT AN OPT-OUT, less the generated
// modules; and a scope that is all generated has nothing hand-written to mutate.
func TestPythonMutationScopesAnUndeclaredPullToItsHandWrittenPython(t *testing.T) {
	p := newPull(t, "")
	putFile(t, p.in.Root, "src/gen.py", "# a header\n# GENERATED by protoc\nx = 1\n")
	p.first(rule{"--name-only --diff-filter=AM", "src/x.py\nsrc/gen.py\nsrc/pkg\n", 0, ""})
	if v := p.run(); v.State != 0 {
		t.Fatalf("%s", said(v))
	}
	c, _ := p.call("--name-only --diff-filter=AM")
	if strings.Join(c.Args, " ") != "diff --name-only --diff-filter=AM since0 HEAD -- *.py :(exclude,glob)**/tests/** :(exclude,glob)**/test_*.py :(exclude,glob)**/*_test.py :(exclude,glob)**/conftest.py" {
		t.Errorf("the names: %q", c.Args)
	}
	// The whole tree is diffed with renames paired, and narrowed to the kept
	// modules in Go (checks.PythonDiffFor): a pathspec cannot see an old path.
	if d, _ := p.call("git diff -M"); strings.Join(d.Args, " ") != "diff -M --unified=0 since0 HEAD" {
		t.Errorf("the diff: %q", d.Args)
	}
	// the generated module is left out of the config, a directory is kept
	if raw, _ := os.ReadFile(filepath.Join(p.work, "python", "src", "cosmic-ray.toml")); strings.Contains(string(raw), "gen.py") || !strings.Contains(string(raw), "src/pkg") {
		t.Errorf("cosmic-ray.toml:\n%s", raw)
	}

	q := newPull(t, "")
	putFile(t, q.in.Root, "src/gen.py", "# Code generated by x. DO NOT EDIT.\n")
	q.first(rule{"--name-only --diff-filter=AM", "src/gen.py\n", 0, ""})
	if v := q.run(); v.State != 0 || q.ran("git diff -M") {
		t.Errorf("an all-generated scope went on to diff: %s", said(v))
	}
}

func TestPythonMutationStandsDownOrCannotRun(t *testing.T) {
	const baseNeedle = "rev-parse --verify --quiet abc123^{commit}"
	for _, tc := range []struct {
		name   string
		base   string
		script func(p *pull)
		state  int
		// reason is checked off a finding or could-not-run: a pass keeps no output
		// (checks.VerdictOf), so a stand-down is told apart by what ran.
		reason  []string
		reached []string
		never   []string
	}{
		{"no base", "-", nil, 0, nil, nil, []string{baseNeedle, "uv --version"}},
		{"a base the history lacks", "abc123", func(p *pull) { p.first(rule{baseNeedle, "", 1, ""}) }, 0, nil,
			[]string{baseNeedle}, []string{"merge-base", "--name-only"}},
		{"no merge base", "abc123", func(p *pull) { p.first(rule{"merge-base", "", 1, ""}) }, 2,
			[]string{"no merge base between the base abc123 and HEAD (exit 1)"}, []string{"merge-base"}, []string{"--name-only"}},
		{"a merge base that answers nothing", "abc123", func(p *pull) { p.first(rule{"merge-base", " \n", 0, ""}) }, 2,
			[]string{"no merge base between the base abc123 and HEAD (exit 0)"}, nil, []string{"--name-only"}},
		{"no python changed", "abc123", func(p *pull) { p.first(rule{"--name-only --diff-filter=AM", "", 0, ""}) }, 0, nil,
			[]string{"--name-only"}, []string{"git diff -M"}},
		{"git cannot list the python", "abc123", func(p *pull) { p.first(rule{"--name-only --diff-filter=AM", "bad", 1, ""}) }, 2,
			[]string{"git could not list the python the pull changed: bad"}, nil, []string{"git diff -M"}},
		{"lines only removed", "abc123", func(p *pull) {
			p.first(rule{"diff -M --unified=0", "diff --git a/src/x.py b/src/x.py\n+++ b/src/x.py\n@@ -1 +0,0 @@\n-x = 1", 0, ""})
		}, 0, nil, []string{"git diff -M"}, []string{"uv sync"}},
		{"a module moved whole", "abc123", func(p *pull) {
			p.first(rule{"diff -M --unified=0", "diff --git a/old/x.py b/src/x.py\nsimilarity index 100%\nrename from old/x.py\nrename to src/x.py", 0, ""})
		}, 0, nil, []string{"git diff -M"}, []string{"uv sync"}},
		{"git cannot diff", "abc123", func(p *pull) { p.first(rule{"diff -M --unified=0", "bad object", 1, ""}) }, 2,
			[]string{"git could not diff the pull against its base since0: bad object"}, nil, []string{"uv sync"}},
		{"the sync fails", "abc123", func(p *pull) {
			p.first(rule{"uv sync --all-extras --locked", "error: the lockfile needs to be updated", 2, ""})
		}, 2, []string{"uv sync --all-extras --locked exited 2", "lockfile needs to be updated"}, nil, []string{"cosmic-ray init"}},
		{"init enumerates nothing", "abc123", func(p *pull) { p.first(rule{"cosmic-ray init", "", 1, ""}) }, 2,
			[]string{"cosmic-ray init enumerated no mutation sites (exit 1)"}, nil, []string{"testkit scope", "forge-testkit-mutation scope"}},
		{"the diff matches no site", "abc123", func(p *pull) {
			p.first(rule{"forge-testkit-mutation scope", "ValueError: scope matched none of 797 mutation sites — refusing to run a vacuous gate", 1, ""})
		}, 1, []string{"forge-testkit-mutation scope exited 1", "check critical-modules", "scope matched none of 797"}, nil, []string{"forge-testkit-mutation plugin"}},
		// A scope that failed for any other reason (uv could not resolve the
		// testkit, the CLI crashed) measured nothing: could-not-run, never a
		// finding the star is told to fix in its declaration.
		{"scope cannot resolve the testkit", "abc123", func(p *pull) {
			p.first(rule{"forge-testkit-mutation scope", "No solution found when resolving tool dependencies: forge-testkit>=2.1.0 was not found in the package registry", 1, ""})
		}, 2, []string{"CANNOT RUN", "the scope step", "No solution found"}, nil, []string{"forge-testkit-mutation plugin"}},
		{"scope crashes", "abc123", func(p *pull) {
			p.first(rule{"forge-testkit-mutation scope", "Traceback (most recent call last):", 1, ""})
		}, 2,
			[]string{"CANNOT RUN", "the scope step"}, nil, []string{"forge-testkit-mutation plugin"}},
		{"nothing mutable", "abc123", func(p *pull) { p.scopeOut = "kept=0\nunmutable=true\n" }, 0, nil,
			[]string{"forge-testkit-mutation scope"}, []string{"forge-testkit-mutation plugin"}},
		{"the plugin cannot be written", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation plugin", "", 1, ""}) }, 2,
			[]string{"the test-selection plugin could not be written"}, nil, []string{"forge-testkit-mutation plan"}},
		{"the plan names no workers", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation plan", "pending=0\n", 0, ""}) }, 2,
			[]string{"the session could not be planned"}, nil, []string{"uv run --with coverage"}},
		{"the plan fails", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation plan", "", 1, ""}) }, 2,
			[]string{"the session could not be planned (exit 1)"}, nil, []string{"uv run --with coverage"}},
		{"the unmutated suite fails", "abc123", func(p *pull) {
			p.first(rule{"uv run --with coverage", "collected 3 items\nFAILED tests/test_x.py::test_a\n1 failed", 1, ""})
		}, 2, []string{"the unmutated suite exited 1 in worker 1", "1 failed"}, nil, []string{"forge-testkit-mutation map"}},
		{"the unmutated suite runs out of memory", "abc123", func(p *pull) {
			p.first(rule{"uv run --with coverage", "E   MemoryError", 1, ""})
		}, 2, []string{"ran out of memory under the 4G ceiling"}, nil, []string{"forge-testkit-mutation map"}},
		{"the map cannot be built", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation map", "", 1, ""}) }, 2,
			[]string{"the test map could not be built"}, nil, []string{"forge-testkit-mutation partition"}},
		{"the session cannot be partitioned", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation partition", "", 1, ""}) }, 2,
			[]string{"the session could not be partitioned across 2 workers"}, nil, []string{"cosmic-ray exec"}},
		{"a worker's exec breaks", "abc123", func(p *pull) { p.first(rule{"cosmic-ray exec", "", 3, ""}) }, 2,
			[]string{"cosmic-ray exec exited 3"}, []string{"forge-testkit-mutation merge"}, []string{"forge-testkit-mutation selection"}},
		{"a worker leaves jobs unscored", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation merge", "", 1, ""}) }, 2,
			[]string{"cosmic-ray exec exited 1"}, nil, []string{"forge-testkit-mutation selection"}},
		{"a mutant run measured nothing", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation selection", "", 3, ""}) }, 2,
			[]string{"matched none of its selected tests"}, nil, []string{"forge-testkit-mutation report"}},
		{"an unreadable selection summary", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation selection", "", 1, ""}) }, 0, nil,
			[]string{"forge-testkit-mutation report"}, nil},
		{"no mutants ran", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation report", "", 1, "no mutants ran"}) }, 2,
			[]string{"no mutants ran — the scope kept sites"}, nil, nil},
		{"an unreadable session", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation report", "", 2, ""}) }, 2,
			[]string{"forge-testkit-mutation report exited 2"}, nil, nil},
		// a program that did not start, or was killed, never ran
		{"no prlimit in the image", "abc123", func(p *pull) { p.first(rule{"prlimit --version", "exec: prlimit: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: exec: prlimit: not found"}, nil, []string{"--name-only"}},
		{"no uv in the image", "abc123", func(p *pull) { p.first(rule{"uv --version", "exec: uv: not found", 127, ""}) }, 2,
			[]string{"the atom never ran: exit code: 127", "failed: uv --version"}, nil, []string{"merge-base"}},
		{"the base check never ran", "abc123", func(p *pull) { p.first(rule{baseNeedle, "git: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: git: not found"}, nil, []string{"merge-base"}},
		{"the names never ran", "abc123", func(p *pull) { p.first(rule{"--name-only --diff-filter=AM", "git: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: git: not found"}, nil, []string{"git diff -M"}},
		{"the names were killed", "abc123", func(p *pull) { p.first(rule{"--name-only --diff-filter=AM", "Killed", 137, ""}) }, 2,
			[]string{"the atom never ran: exit code: 137"}, nil, []string{"git diff -M"}},
		{"the diff never ran", "abc123", func(p *pull) { p.first(rule{"diff -M --unified=0", "git: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: git: not found"}, nil, []string{"uv sync"}},
		{"the sync never ran", "abc123", func(p *pull) { p.first(rule{"uv sync", "uv: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: uv: not found"}, nil, []string{"cosmic-ray init"}},
		{"init never ran", "abc123", func(p *pull) { p.first(rule{"cosmic-ray init", "uv: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: uv: not found"}, nil, []string{"forge-testkit-mutation scope"}},
		{"scope never ran", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation scope", "uv: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: uv: not found"}, nil, []string{"forge-testkit-mutation plugin"}},
		{"the plugin never ran", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation plugin", "uv: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: uv: not found"}, nil, []string{"forge-testkit-mutation plan"}},
		{"the plan never ran", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation plan", "uv: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: uv: not found"}, nil, []string{"uv run --with coverage"}},
		{"a measure never ran", "abc123", func(p *pull) { p.first(rule{"uv run --with coverage", "uv: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: uv: not found"}, nil, []string{"forge-testkit-mutation map"}},
		{"the map never ran", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation map", "uv: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: uv: not found"}, nil, []string{"forge-testkit-mutation partition"}},
		{"partition never ran", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation partition", "uv: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: uv: not found"}, nil, []string{"cosmic-ray exec"}},
		{"an exec never ran", "abc123", func(p *pull) { p.first(rule{"cosmic-ray exec", "uv: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: uv: not found"}, nil, []string{"forge-testkit-mutation merge"}},
		{"the merge never ran", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation merge", "uv: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: uv: not found"}, nil, []string{"forge-testkit-mutation selection"}},
		{"selection never ran", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation selection", "uv: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: uv: not found"}, nil, []string{"forge-testkit-mutation report"}},
		{"the report never ran", "abc123", func(p *pull) { p.first(rule{"forge-testkit-mutation report", "uv: not found", -1, ""}) }, 2,
			[]string{"the atom never ran: uv: not found"}, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPull(t, "")
			if tc.base == "-" {
				p.in.Base = ""
			}
			if tc.script != nil {
				tc.script(p)
			}
			v := p.run()
			if v.State != tc.state {
				t.Fatalf("state %d (%s), want %d:\n%s", v.State, v.Result, tc.state, said(v))
			}
			for _, w := range tc.reason {
				if !strings.Contains(said(v), w) {
					t.Errorf("the verdict lacks %q:\n%s", w, said(v))
				}
			}
			for _, n := range tc.reached {
				if !p.ran(n) {
					t.Errorf("never reached %s", n)
				}
			}
			for _, n := range tc.never {
				if p.ran(n) {
					t.Errorf("went on to %s: %v", n, p.runs)
				}
			}
		})
	}
}

// A missing base the door named is LOUD on the door's own tree (a fetched tree),
// and a stand-down on a local snapshot.
func TestPythonMutationFailsLoudOnAFetchedTreeMissingItsBase(t *testing.T) {
	p := newPull(t, "")
	p.first(rule{"rev-parse --verify", "", 1, ""})
	t.Setenv("ATOM_ORIGIN", "https://door/x")
	if v := p.run(); v.State != 2 || !strings.Contains(said(v), "CANNOT RUN - the base abc123 is not in this history, though the door named it and it was fetched") {
		t.Fatalf("%s", said(v))
	}
}

// failing makes the named disk operation fail for paths that contain needle.
func failing(t *testing.T, op, needle string) {
	t.Helper()
	oldRemove, oldCopy, oldWrite, oldRead := removeAll, copyTree, writeFile, readFile
	t.Cleanup(func() { removeAll, copyTree, writeFile, readFile = oldRemove, oldCopy, oldWrite, oldRead })
	switch op {
	case "remove":
		removeAll = func(p string) error {
			if strings.Contains(p, needle) {
				return os.ErrPermission
			}
			return oldRemove(p)
		}
	case "copy":
		copyTree = func(src, dst string, skip ...string) error {
			if strings.Contains(dst, needle) {
				return os.ErrPermission
			}
			return oldCopy(src, dst, skip...)
		}
	case "mkdir":
		mkdirAll = func(p string, m os.FileMode) error {
			if strings.HasSuffix(p, needle) {
				return os.ErrPermission
			}
			return os.MkdirAll(p, m)
		}
		t.Cleanup(func() { mkdirAll = os.MkdirAll })
	case "write":
		writeFile = func(p string, b []byte, m os.FileMode) error {
			if strings.HasSuffix(p, needle) {
				return os.ErrPermission
			}
			return oldWrite(p, b, m)
		}
	case "read":
		readFile = func(p string) ([]byte, error) {
			if strings.HasSuffix(p, needle) {
				return nil, os.ErrPermission
			}
			return oldRead(p)
		}
	}
}

// Work the atom cannot lay down is its could-not-run, said as such, and nothing
// runs after it.
func TestPythonMutationCannotRunWhereItCannotWrite(t *testing.T) {
	for _, tc := range []struct {
		name, op, needle string
		never            string
	}{
		{"the work directory cannot be cleared", "remove", "/python", "uv sync"},
		{"the tree cannot be copied", "copy", "/python/src", "uv sync"},
		{"a directory for the config cannot be made", "mkdir", "/python", "cosmic-ray init"},
		{"the config cannot be written", "write", "/python/pr.diff", "cosmic-ray init"},
		{"the scope output cannot be made", "write", "/python/scope.out", "forge-testkit-mutation scope"},
		{"a worker's tree cannot be copied", "copy", "/python/w1", "uv run --with coverage"},
		{"a worker's config cannot be written", "write", "/python/w1/cosmic-ray.toml", "forge-testkit-mutation partition"},
		{"a worker's share cannot be read", "read", "/python/w1.sqlite", "forge-testkit-mutation merge"},
		{"a worker's share cannot be laid down", "write", "/python/w1/session.sqlite", "forge-testkit-mutation merge"},
		{"a worker's result cannot be read", "read", "/python/w1/session.sqlite", "forge-testkit-mutation merge"},
		{"a worker's result cannot be kept", "write", "/python/parts/w1.sqlite", "forge-testkit-mutation merge"},
		{"the joined selection log cannot be written", "write", "/python/select.log", "forge-testkit-mutation merge"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPull(t, "")
			failing(t, tc.op, tc.needle)
			v := p.run()
			if v.State != 2 || !strings.Contains(said(v), "CANNOT RUN - the atom never ran") {
				t.Errorf("%s", said(v))
			}
			if p.ran(tc.never) {
				t.Errorf("went on to %s", tc.never)
			}
		})
	}
}

// A worker whose environment will not sync cannot measure anything, and the
// verdict says so in the sync's words.
func TestPythonMutationCannotRunWhenAWorkerWillNotSync(t *testing.T) {
	p := newPull(t, "")
	p.failSyncFrom = 2 // the primary synced; the workers do not
	v := p.run()
	if v.State != 2 || !strings.Contains(said(v), "uv sync --all-extras --locked exited 2") || p.ran("uv run --with coverage") {
		t.Errorf("%s", said(v))
	}
}

// A WORKER'S TEST RUN IS SIZED BY ITS SHARE OF THE CPU BUDGET. `pytest -n auto`
// takes the node's core count, so each of the lane's workers would start a
// process per node core (24 on dev01) inside a container capped at a few: the
// share is told to xdist, in the measure and in the exec.
func TestPythonMutationWorkersTellXdistTheirShareOfTheBudget(t *testing.T) {
	for budget, want := range map[string]string{"8": "4", "4": "2", "3": "1", "1": "1"} {
		p := newPull(t, "critical_modules: src/x.py\n")
		t.Setenv(checks.CPUsEnv, budget)
		if v := p.run(); v.State != 0 {
			t.Fatalf("%s", said(v))
		}
		seen := 0
		for j, cmd := range p.cmds {
			if strings.Contains(p.runs[j], "uv run --with coverage") || strings.Contains(p.runs[j], "cosmic-ray exec") {
				seen++
				if env := strings.Join(cmd.Env, "\n"); !strings.Contains(env, "PYTEST_XDIST_AUTO_NUM_WORKERS="+want+"\n") && !strings.HasSuffix(env, "PYTEST_XDIST_AUTO_NUM_WORKERS="+want) {
					t.Errorf("budget %s, %s: want xdist workers %s in\n%s", budget, p.runs[j], want, env)
				}
			}
		}
		if seen != 4 {
			t.Errorf("budget %s: %d worker runs seen, want 2 measures and 2 execs", budget, seen)
		}
	}
}
