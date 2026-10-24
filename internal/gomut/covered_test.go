package gomut

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/gitextra"
)

// testOnly is a pull that changed only a_test.go's TestA, whose run covers
// a.go:2 (the profile the fake `go test` writes).
func testOnly(t *testing.T, profile string, goTestCode int) *pull {
	t.Helper()
	p := newPull(t, ".")
	orig := lookGit
	t.Cleanup(func() { lookGit = orig })
	lookGit = func() (string, error) { return "/usr/bin/git", nil }
	putFile(t, p.in.Root, "a_test.go", "package x\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tf()\n}\n\nfunc TestB(t *testing.T) {}\n")
	p.first(rule{"git diff --relative since0 --name-only", "a_test.go\n", 0, ""})
	p.first(rule{"--unified=0 --no-color --no-ext-diff since0 HEAD -- a_test.go", "+++ b/a_test.go\n@@ -6 +6 @@\n+\tf()\n", 0, ""})
	p.asked = map[string]func(int) (string, int){
		"go test -count=1 -run": func(int) (string, int) {
			if profile != "" {
				putFile(t, filepath.Join(p.work, "go", "root", "testcov"), "root.out", profile)
			}
			return "ok", goTestCode
		},
	}
	return p
}

const coveredProfile = "mode: set\nx/a.go:2.1,2.10 1 1\nx/a.go:4.1,4.5 1 0\n"

// A test-only change that kills an existing survivor reports it gone: the line
// its test runs was mutated, and nothing survived.
func TestCoveredATestOnlyChangeKillsASurvivor(t *testing.T) {
	p := testOnly(t, coveredProfile, 0)
	p.report = `{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":2,"column":3}]}]}`
	v := p.run()
	c, ok := p.call("-changed-since")
	if !ok || envOf(c, gitextra.EnvRanges) != "a.go:2-2" {
		t.Fatalf("gomutants' extra = %q (ran %v): %v", envOf(c, gitextra.EnvRanges), ok, p.runs)
	}
	if run, _ := p.call("go test -count=1"); !slices.Contains(run.Args, "^(TestA)$") || !slices.Contains(run.Args, "-coverpkg=./...") {
		t.Fatalf("the changed test was not run alone under coverage: %v", run.Args)
	}
	if v.State != 0 || len(v.Findings) != 0 || !strings.Contains(said(v), "changed tests: 1 source line(s) in 1 file(s) they run joined the scope") {
		t.Fatalf("%d findings: %s", len(v.Findings), said(v))
	}
}

// A test-only change that kills nothing re-reports the survivor.
func TestCoveredATestOnlyChangeThatKillsNothingReReportsTheSurvivor(t *testing.T) {
	p := testOnly(t, coveredProfile, 0)
	p.report = survivors
	v := p.run()
	if v.State != 1 || !slices.ContainsFunc(v.Findings, func(f checks.Finding) bool { return f.Subject == "a.go:2" }) {
		t.Fatalf("the survivor is not re-reported: %+v", v.Findings)
	}
}

// The test file itself is never in the scope: its own lines yield no mutants.
func TestCoveredTheTestFileIsNeverMutated(t *testing.T) {
	p := testOnly(t, coveredProfile+"x/a_test.go:5.1,7.2 1 1\n", 0)
	p.run()
	c, _ := p.call("-changed-since")
	if got := envOf(c, gitextra.EnvRanges); strings.Contains(got, "_test.go") || got != "a.go:2-2" {
		t.Fatalf("gomutants' extra = %q", got)
	}
}

// The bound cuts whole files and says so; the cut is not a failure.
func TestCoveredTheBoundCutsAndSays(t *testing.T) {
	t.Setenv(EnvCoveredCap, "1")
	p := testOnly(t, coveredProfile+"x/b.go:1.1,3.2 1 1\n", 0)
	v := p.run()
	if v.State == 2 || !strings.Contains(said(v), "3 line(s) in 1 file(s) cut at the "+EnvCoveredCap+" bound (b.go)") {
		t.Fatalf("%s", said(v))
	}
	if c, _ := p.call("-changed-since"); envOf(c, gitextra.EnvRanges) != "a.go:2-2" {
		t.Fatalf("extra = %q", envOf(c, gitextra.EnvRanges))
	}
}

// Changed tests that fail still lend the coverage they wrote; ones that wrote
// none add nothing, and neither is a failure of the run.
func TestCoveredFailingOrSilentTestsAreSaidNotFailed(t *testing.T) {
	p := testOnly(t, coveredProfile, 1)
	v := p.run()
	if v.State == 2 || !strings.Contains(said(v), "changed tests in . exited 1; the coverage they wrote was read") {
		t.Fatalf("%s", said(v))
	}
	p = testOnly(t, "", 2)
	v = p.run()
	if v.State == 2 || !strings.Contains(said(v), "changed tests in . wrote no coverage (exit 2)") {
		t.Fatalf("%s", said(v))
	}
	p = testOnly(t, coveredProfile, 0)
	p.first(rule{"--unified=0 --no-color --no-ext-diff since0 HEAD -- a_test.go", "fatal", 128, ""})
	if v := p.run(); !strings.Contains(said(v), "git could not diff them") {
		t.Fatalf("%s", said(v))
	}
}

// A pull that changed no test runs no test for its scope.
func TestCoveredNoChangedTestRunsNothing(t *testing.T) {
	p := newPull(t, ".")
	p.run()
	if p.ran("go test -count=1 -run") {
		t.Fatal("a change with no test file ran tests for its scope")
	}
	if checks.MutationCoveredCap() != checks.DefaultMutationCoveredCap {
		t.Fatal("default bound")
	}
}

// A changed test file that declares no test runs nothing; a work directory that
// cannot hold the profile is said, not failed.
func TestCoveredAFileWithNoTestsAndAnUnwritableProfile(t *testing.T) {
	p := testOnly(t, coveredProfile, 0)
	putFile(t, p.in.Root, "a_test.go", "package x\n\nfunc helper() {}\n")
	p.run()
	if p.ran("go test -count=1 -run") {
		t.Fatal("a test file with no test ran tests")
	}
	p = testOnly(t, coveredProfile, 0)
	putFile(t, filepath.Join(p.work, "go", "root"), "testcov", "a file where the directory goes")
	if v := p.run(); v.State == 2 || !strings.Contains(said(v), "changed tests in .: mkdir") {
		t.Fatalf("%s", said(v))
	}
}

func TestLookGitAsksThePath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := lookGit(); err == nil {
		t.Fatal("a PATH with no git found one")
	}
}
