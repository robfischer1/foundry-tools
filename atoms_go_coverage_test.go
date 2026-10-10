package main

import (
	"context"
	"strings"
	"testing"
)

// The chain of the go:diff-coverage atom. The judgement — which lines are uncovered — is
// internal/checks' (godiffcoverage_test.go); these pin what the atom asks, in
// what order, and what it does with each answer.

const (
	// covDiffNeedle is the change set's own diff, asked in the module.
	covDiffNeedle = `"git","diff","--unified=0","--no-color","--no-ext-diff","--relative","since0","--","*.go"`
	// covListNeedle is the listing that places the profile's packages.
	covListNeedle = `"go","list","-e","-f"`
	// covCountNeedle is go:test-race's test count, which runs before its suite.
	covCountNeedle = `"go","list","-f","{{len`
	covSuiteNeedle = `"go","test","-race"`
	covProfileRead = `file(path:"/tmp/go-test-race.cover"){contents}`
	covSrc         = "package x\n\nfunc F(x int) int {\n\tif x > 0 {\n\t\treturn 1\n\t}\n\treturn 0\n}\n"
	// covProfileText: F ran with x <= 0, so the if-body on line 5 never did.
	covProfileText = "mode: atomic\nx/f.go:3.19,4.11 1 1\nx/f.go:4.11,6.3 1 0\nx/f.go:7.2,7.10 1 1\n"
)

// scriptDiffCoverage answers a pull that added f.go, whose line 5 no test runs.
func scriptDiffCoverage(t *testing.T) {
	t.Helper()
	engine.reset()
	engine.withTree(map[string]string{
		"go.mod": "module x\n\ngo 1.26\n", "f.go": covSrc, "f_test.go": "package x\n",
		"/tmp/go-test-race.cover": covProfileText,
	})
	engine.stdout(mergeBaseNeedle, sinceSha+"\n")
	engine.stdout(covDiffNeedle, "diff --git a/f.go b/f.go\n--- /dev/null\n+++ b/f.go\n@@ -0,0 +1,8 @@\n")
	engine.stdout(covCountNeedle, "11\n")
	engine.stdout(covListNeedle, "x\t/src\n")
}

func TestGoDiffCoverageNamesEachChangedLineNoTestRuns(t *testing.T) {
	scriptDiffCoverage(t)
	v := runAtom(t, "go:diff-coverage", "abc123")
	wantState(t, v, 1, "1 changed line(s) no test executes", "  f.go:5")
	if len(v.Findings) != 1 || v.Findings[0].Subject != "f.go:5" || v.Findings[0].Cause != "not-covered" || v.Findings[0].Verdict != "violated" {
		t.Errorf("findings: %+v", v.Findings)
	}
	// The diff is asked on a container that carries the base; the suite's
	// does not (rule 8), and it writes the profile the atom reads.
	wantCalls(t, engine.chain(covDiffNeedle, "stdout"),
		[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"abc123"`},
		[]string{"withExec", "expect:ANY", "args:[" + covDiffNeedle + "]"})
	suite := engine.chain(covSuiteNeedle, covProfileRead)
	wantCalls(t, suite,
		[]string{"withMountedDirectory", `path:"/dies"`},
		[]string{"withExec", "expect:ANY", `args:["go","test","-race","-coverprofile","/tmp/go-test-race.cover","./..."]`})
	if strings.Contains(suite, "GATE_BASE") {
		t.Errorf("the suite carries the base, so its cache key is the pull's:\n%s", suite)
	}
	wantCalls(t, engine.chain(covListNeedle, "stdout"),
		[]string{"withExec", "expect:ANY", `args:["go","list","-e","-f","{{.ImportPath}}{{\"\\t\"}}{{.Dir}}{{\"\\n\"}}","./..."]`})
}

func TestGoDiffCoveragePassesWhenEveryChangedLineRan(t *testing.T) {
	scriptDiffCoverage(t)
	engine.withTree(map[string]string{"/tmp/go-test-race.cover": strings.ReplaceAll(covProfileText, " 0\n", " 2\n")})
	v := runAtom(t, "go:diff-coverage", "abc123")
	wantState(t, v, 0, "go:diff-coverage: PASS")
	if v.Result != "pass" || v.Findings != nil || strings.Join(v.Logs, "\n") != "go:diff-coverage: every changed line in 1 Go file(s) ran under the suite" {
		t.Errorf("a clean pull: %+v", v)
	}
}

// ONE SUITE FOR BOTH ATOMS. go:test-race and go:diff-coverage share a run in
// the vector, and the second to ask reads the first one's answer.
func TestGoDiffCoverageAndTheRaceSuiteShareOneRun(t *testing.T) {
	scriptDiffCoverage(t)
	r := newRun(dag.Directory(), "", "abc123")
	race := registry["go:test-race"](context.Background(), r)
	cov := registry["go:diff-coverage"](context.Background(), r)
	wantState(t, race, 0)
	wantState(t, cov, 1, "f.go:5")
	runs := 0
	for _, q := range engine.chains() {
		if strings.Contains(q, covSuiteNeedle) && strings.HasSuffix(strings.TrimRight(q, "} "), "exitCode") {
			runs++
		}
	}
	if runs != 1 {
		t.Errorf("the suite was evaluated %d times for two atoms, want 1", runs)
	}
	// Per module: a second module's suite is its own.
	root, again := r.raceSuite(context.Background(), "."), r.raceSuite(context.Background(), ".")
	if root != again || root == r.raceSuite(context.Background(), "tools/forge") {
		t.Error("the run keeps one suite per module")
	}
}

// Every way the atom stands down or cannot run, and that it asks for nothing
// past the point that decided it.
func TestGoDiffCoverageStandsDownOrCannotRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		base   string
		script func()
		state  int
		absent bool
		want   string
		never  string
	}{
		{"no base", "", func() {}, 0, true, "ABSENT - no usable PR base sha", covDiffNeedle},
		{"a base the history does not reach, locally", "abc123", func() { engine.exitCode(`"git","rev-parse"`, 1) }, 0, true,
			"ABSENT - no usable PR base sha", covDiffNeedle},
		{"the base cannot be resolved", "abc123", func() { engine.fail(`"git","rev-parse"`, "engine went away") }, 2, false,
			"CANNOT RUN - the atom never ran: ", covDiffNeedle},
		{"git cannot diff", "abc123", func() {
			engine.exitCode(covDiffNeedle, 2)
			engine.stdout(covDiffNeedle, "")
			engine.stderr(covDiffNeedle, "bad object")
		}, 2, false,
			"CANNOT RUN - the pull could not be diffed against its base since0: git diff exited 2\nbad object", covSuiteNeedle},
		{"the diff never ran", "abc123", func() { engine.fail(covDiffNeedle, "lost") }, 2, false,
			"CANNOT RUN - the pull could not be diffed against its base since0: git diff never ran: ", covSuiteNeedle},
		{"no judged Go changed", "abc123", func() { engine.stdout(covDiffNeedle, "--- a/f_test.go\n+++ b/f_test.go\n@@ -1 +1 @@\n") }, 0, true,
			"ABSENT - this pull adds no line to a non-test, non-generated Go file of this module", covSuiteNeedle},
		{"the suite failed", "abc123", func() { engine.exitCode(covSuiteNeedle, 1) }, 0, true,
			"ABSENT - go:test-race did not pass (findings)", covListNeedle},
		{"the suite could not run", "abc123", func() { engine.fail(covCountNeedle, "engine went away") }, 0, true,
			"ABSENT - go:test-race did not pass (cannot-run)", covSuiteNeedle},
		{"no profile", "abc123", func() { engine.failLeaf(covProfileRead, "contents", "no such file") }, 2, false,
			"CANNOT RUN - the suite passed and its coverage profile could not be read: ", covListNeedle},
		{"the packages cannot be listed", "abc123", func() { engine.exitCode(covListNeedle, 1) }, 2, false,
			"CANNOT RUN - the module's packages could not be listed, so the profile cannot be placed: go list exited 1", ""},
		{"a changed file the tree lacks", "abc123", func() {
			engine.stdout(covDiffNeedle, "--- /dev/null\n+++ b/g.go\n@@ -0,0 +1 @@\n")
		}, 2, false, "CANNOT RUN - a changed file could not be read from the tree: g.go", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scriptDiffCoverage(t)
			tc.script()
			v := runAtom(t, "go:diff-coverage", tc.base)
			wantState(t, v, tc.state, "go:diff-coverage")
			if got := strings.Join(v.Logs, "\n"); !strings.Contains(got, "go:diff-coverage: "+tc.want) {
				t.Errorf("logs %q lack %q", got, tc.want)
			}
			if (v.Result == "absent") != tc.absent || v.Findings != nil {
				t.Errorf("result %s, findings %v", v.Result, v.Findings)
			}
			if tc.never != "" && engine.chain(tc.never) != "" {
				t.Errorf("went on to %s", tc.never)
			}
		})
	}
}

// THE DOOR'S TREE NAMED ITS BASE AND FETCHED IT: a base it still cannot reach
// is a could-not-run there, not a stand-down (run.missingBase).
func TestGoDiffCoverageOnTheDoorsTreeCannotRunWithoutItsBase(t *testing.T) {
	scriptDiffCoverage(t)
	engine.exitCode(`"git","rev-parse"`, 1)
	v := registry["go:diff-coverage"](context.Background(), newRun(dag.Directory(), "https://door/x", "abc123"))
	wantState(t, v, 2, "go:diff-coverage: CANNOT RUN - the base abc123 is not in this history")
}

// A NESTED MODULE IS HELD AGAINST ITS OWN SUITE, its findings reach the
// folded verdict (a fold keeps states and reasons, not findings), and their
// subjects are named from the repository root.
func TestGoDiffCoverageInANestedModuleKeepsItsFindings(t *testing.T) {
	scriptDiffCoverage(t)
	engine.reset()
	engine.withTree(nestedTree)
	engine.withTree(map[string]string{"tools/forge/f.go": covSrc, "/tmp/go-test-race.cover": covProfileText})
	engine.stdout(mergeBaseNeedle, sinceSha+"\n")
	engine.stdout(covDiffNeedle, "--- /dev/null\n+++ b/f.go\n@@ -0,0 +1,8 @@\n")
	engine.stdout(covCountNeedle, "11\n")
	engine.stdout(covListNeedle, "x\t/src/tools/forge\n")
	v := runAtom(t, "go:diff-coverage", "abc123")
	wantState(t, v, 1, "FINDINGS in 1 of 1 Go module (tools/forge)", "  tools/forge/f.go:5")
	if len(v.Findings) != 1 || v.Findings[0].Subject != "tools/forge/f.go:5" {
		t.Errorf("findings: %+v", v.Findings)
	}
	wantCalls(t, engine.chain(covDiffNeedle, "stdout"), []string{"withWorkdir", `path:"/src/tools/forge"`})
	wantCalls(t, engine.chain(covSuiteNeedle, covProfileRead), []string{"withWorkdir", `path:"/src/tools/forge"`})
}
