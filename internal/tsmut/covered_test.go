package tsmut

import (
	"os"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// testOnly is a pull that changed only src/gate.test.ts, whose run covers
// src/gate.ts:1-2 and src/util.ts:4 (the lcov the fake `bun test` writes). The
// test file's own lines are in the report, as bun writes them.
func testOnly(t *testing.T, answers string) *pull {
	t.Helper()
	p := newPull(t, answers)
	putFile(t, p.in.Root, "src/gate.ts", "a\nb\nc\n")
	putFile(t, p.in.Root, "src/util.ts", "1\n2\n3\n4\n")
	putFile(t, p.in.Root, "src/gate.test.ts", "test\n")
	p.first(rule{"--name-only --diff-filter=AM", "src/gate.test.ts\n", 0})
	p.first(rule{"git diff --unified=0", "", 0})
	p.lcov = "TN:\nSF:src/gate.ts\nDA:1,1\nDA:2,3\nDA:3,0\nend_of_record\n" +
		"SF:src/gate.test.ts\nDA:1,1\nend_of_record\n" +
		"SF:src/util.ts\nDA:4,2\nend_of_record\n" +
		"SF:node_modules/x/index.ts\nDA:1,1\nend_of_record\n"
	return p
}

// A test-only change is mutation-tested by the source its tests run: the changed
// test runs alone under bun's coverage, after the install, and the source lines
// it ran are handed to Stryker; the test file itself never is.
func TestCoveredATestOnlyTSChangeMutatesTheSourceItsTestsRun(t *testing.T) {
	p := testOnly(t, "")
	v := p.run()
	if strings.Contains(said(v), "nothing to mutate") {
		t.Fatalf("a test-only pull took the empty-change-set exit: %s", said(v))
	}
	run, ok := p.call("bun test --coverage")
	if !ok || !slices.Contains(run.Args, "./src/gate.test.ts") || !slices.Contains(run.Args, "lcov") {
		t.Fatalf("the changed test was not run alone under coverage: %v\n%s", run.Args, strings.Join(p.runs, "\n"))
	}
	c, ok := p.call("--mutate")
	line := strings.Join(c.Args, " ")
	if !ok || !strings.Contains(line, "src/gate.ts:1-2") || !strings.Contains(line, "src/util.ts:4-4") {
		t.Fatalf("stryker was handed %q (ran %v)", line, ok)
	}
	for _, never := range []string{"gate.test.ts", "node_modules"} {
		if strings.Contains(line, never) {
			t.Errorf("stryker was handed %s: %q", never, line)
		}
	}
	if !strings.Contains(said(v), "ts:mutation: changed tests: 3 source line(s) in 2 file(s) they run joined the scope") {
		t.Errorf("the verdict does not say what the tests added: %s", said(v))
	}
}

// The bound applies to the covered lines.
func TestCoveredTSLinesAreBoundedByTheCap(t *testing.T) {
	p := testOnly(t, "")
	t.Setenv(checks.EnvMutationCoveredCap, "2")
	v := p.run()
	c, _ := p.call("--mutate")
	if !strings.Contains(said(v), "1 line(s) in 1 file(s) cut at the "+checks.EnvMutationCoveredCap+" bound (src/util.ts)") || strings.Contains(strings.Join(c.Args, " "), "util.ts") {
		t.Fatalf("stryker was handed %v: %s", c.Args, said(v))
	}
}

// Declared critical modules bound the covered lines too.
func TestCoveredTSLinesStayInsideTheDeclaredModules(t *testing.T) {
	p := testOnly(t, "critical_modules: src/gate.ts\n")
	p.run()
	c, _ := p.call("--mutate")
	if line := strings.Join(c.Args, " "); !strings.Contains(line, "src/gate.ts:1-2") || strings.Contains(line, "util.ts") {
		t.Fatalf("stryker was handed %q", line)
	}
}

// Tests that wrote no coverage add nothing, and the pull stands down as it did.
func TestCoveredTSTestsThatWroteNoCoverageAddNothing(t *testing.T) {
	p := testOnly(t, "")
	p.lcov = ""
	v := p.run()
	if v.State != 0 || !strings.Contains(said(v), "nothing to mutate") || !strings.Contains(said(v), "wrote no coverage") {
		t.Fatalf("%s", said(v))
	}
}

// The edges: a listing git refuses, a tree that cannot be listed and tests no
// config owns are said and add nothing; a suite bun fails still adds what it
// covered; a failed install is the run's end.
func TestCoveredTSEdges(t *testing.T) {
	p := testOnly(t, "")
	p.first(rule{"--name-only --diff-filter=AM", "fatal", 128})
	if v := p.run(); !strings.Contains(said(v), "git could not list them") || p.ran("bun test") {
		t.Errorf("%s", said(v))
	}

	p = testOnly(t, "")
	p.in.FilesErr = os.ErrPermission
	if v := p.run(); !strings.Contains(said(v), "Stryker configs could not be listed") {
		t.Errorf("%s", said(v))
	}

	p = testOnly(t, "")
	p.first(rule{"--name-only --diff-filter=AM", "other/a.test.ts\nsrc/gate.ts\n", 0})
	p.in.Files = []string{"package.json"}
	if v := p.run(); !strings.Contains(said(v), "no Stryker config owns them") {
		t.Errorf("%s", said(v))
	}

	p = testOnly(t, "")
	p.first(rule{"bun test --coverage", "1 fail", 1})
	v := p.run()
	if !strings.Contains(said(v), "exited 1 under bun; the coverage they wrote was read") {
		t.Errorf("%s", said(v))
	}

	p = testOnly(t, "")
	p.first(rule{"bun install", "error: lockfile had changes", 1})
	if v := p.run(); v.State == 0 || p.ran("bun test") {
		t.Errorf("%d %s", v.State, said(v))
	}

	p = testOnly(t, "critical_modules: src/\n")
	p.run()
	if c, _ := p.call("--mutate"); !strings.Contains(strings.Join(c.Args, " "), "src/util.ts:4-4") || strings.Contains(strings.Join(c.Args, " "), "test.ts") {
		t.Errorf("a declared directory: %v", c.Args)
	}
}

// A pull that changed no test runs no coverage and installs nothing early.
func TestCoveredATSPullWithNoTestChangeRunsNoCoverage(t *testing.T) {
	p := newPull(t, "")
	p.first(rule{"git diff --unified=0", "", 0})
	if v := p.run(); v.State != 0 || !strings.Contains(said(v), "nothing to mutate") {
		t.Fatalf("%s", said(v))
	}
	if p.ran("bun test") || p.ran("bun install") {
		t.Fatalf("a pull that changed no test ran coverage:\n%s", strings.Join(p.runs, "\n"))
	}
}
