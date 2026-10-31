package gomut

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/gitextra"
)

// THE NIGHTLY'S EXTRA SCOPE (extra.go). A night hands the atom its base (the
// last night's tip) and the rolled-up survivors' lines in the base's numbering.

// carried is a night over the fixture repo: nothing new in Go since the base,
// one survivor carried at a.go:2.
func carried(t *testing.T, ranges string) *pull {
	t.Helper()
	p := newPull(t, ".")
	t.Setenv(checks.EnvMutationRanges, ranges)
	orig := lookGit
	t.Cleanup(func() { lookGit = orig })
	lookGit = func() (string, error) { return "/usr/bin/git", nil }
	p.first(rule{"git diff --relative", "", 0, ""})
	p.first(rule{"diff -M --unified=0", "", 0, ""})
	p.first(rule{"git -C " + p.in.Root + " diff --unified=0", "", 0, ""})
	return p
}

func envOf(c atoms.Cmd, key string) string {
	for _, e := range c.Env {
		if v, ok := strings.CutPrefix(e, key+"="); ok {
			return v
		}
	}
	return ""
}

// A night with no new Go still re-tests its carried survivor: it does not stand
// down as "this pull changes no Go file", and gomutants is handed the line
// through its git.
func TestNightlyACarriedSurvivorIsGradedWhenNothingNewChanged(t *testing.T) {
	p := carried(t, "a.go:2-2")
	p.report = survivors
	v := p.run()
	if strings.Contains(said(v), "changes no Go file") {
		t.Fatalf("a night with a carried survivor stood down: %s", said(v))
	}
	c, ok := p.call("-changed-since")
	if !ok {
		t.Fatalf("gomutants never ran: %v", p.runs)
	}
	if got := envOf(c, gitextra.EnvRanges); got != "a.go:2-2" {
		t.Fatalf("gomutants' git carried %q, want a.go:2-2", got)
	}
	if envOf(c, gitextra.EnvRealGit) != "/usr/bin/git" {
		t.Fatalf("the real git is not named: %v", c.Env)
	}
	// The git that hands it the ranges is linked beside the PATH's first entry.
	if want := "PATH=" + filepath.Join(p.work, "bin") + string(os.PathListSeparator); !slices.ContainsFunc(c.Env, func(e string) bool { return strings.HasPrefix(e, want) }) {
		t.Errorf("the linked git is not first on PATH: %v", c.Env)
	}
	if _, err := os.Lstat(filepath.Join(p.work, "bin", "git")); err != nil {
		t.Errorf("git is not linked: %v", err)
	}
	if v.State != 1 || !slices.ContainsFunc(v.Findings, func(f checks.Finding) bool { return f.Subject == "a.go:2" }) {
		t.Fatalf("the carried survivor that lived again is a finding: %+v", v.Findings)
	}
}

// A test-only change is the kill: the carried line is graded again and the
// survivor is gone from the verdict.
func TestNightlyATestOnlyChangeKillsACarriedSurvivor(t *testing.T) {
	p := carried(t, "a.go:2-2")
	p.first(rule{"git diff --relative", "a_test.go\n", 0, ""})
	p.report = `{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":2,"column":3}]}]}`
	v := p.run()
	if v.State != 0 || len(v.Findings) != 0 {
		t.Fatalf("a killed carried survivor left %d finding(s): %s", len(v.Findings), said(v))
	}
	if c, _ := p.call("-changed-since"); envOf(c, gitextra.EnvRanges) != "a.go:2-2" {
		t.Fatalf("the carried line was not re-tested: %v", c.Env)
	}
}

// An untouched survivor costs nothing: its unit's key is the same as last
// night's, the store answers it, and neither the mutants nor the controls run.
func TestNightlyAnUntouchedCarriedSurvivorCostsNothing(t *testing.T) {
	p := carried(t, "a.go:2-2")
	lived := checks.ScoredMutant{File: "a.go", Line: 2, Col: 3, Op: "T", Outcome: checks.OutcomeLived, Status: "LIVED"}
	srv, asked := door(t, ".", map[string]any{"lane": "mutation", "run_number": 4,
		"counts": map[string]int{"generated": 1, "lived": 1}, "mutants": []checks.ScoredMutant{lived}})
	p.in.Origin = srv.URL + "/rob/x.git"
	t.Setenv(atoms.EnvMutantsToken, "tok")
	v := p.run()
	if p.ran("gomutants") {
		t.Fatalf("an untouched survivor was mutated again: %v", p.runs)
	}
	if !strings.Contains(string(*asked), `"unit":"."`) {
		t.Fatalf("the carried unit was not asked: %s", *asked)
	}
	if v.State != 1 || len(v.Findings) != 1 || v.Findings[0].Subject != "a.go:2" {
		t.Fatalf("the reused survivor is still the finding: %+v %s", v.Findings, said(v))
	}
}

// A carried line the night rewrote drops out of the extra scope (the night's own
// diff re-mutates what replaced it); one that only moved is carried at its new
// line and says where it came from.
func TestNightlyAChangedLineDropsAndAMovedOneIsFollowed(t *testing.T) {
	p := carried(t, "a.go:2-2,a.go:5-5")
	p.first(rule{"diff -M --unified=0", "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -2 +2,2 @@\n-a\n+b\n+c\n", 0, ""})
	p.first(rule{"git diff --relative", "a.go\n", 0, ""})
	v := p.run()
	c, _ := p.call("-changed-since")
	if got := envOf(c, gitextra.EnvRanges); got != "a.go:6-6" {
		t.Fatalf("gomutants' extra = %q, want only the moved line a.go:6-6", got)
	}
	if !slices.ContainsFunc(v.Findings, func(f checks.Finding) bool {
		return f.Cause == checks.MutationCarriedCause && f.Subject == "a.go:6" && f.Detail == "from a.go:5" && f.Probe == id
	}) {
		t.Fatalf("the moved line left no carried finding: %+v", v.Findings)
	}
}

// Ranges the runner should never have sent are a could-not-run, never a silent
// narrowing.
func TestNightlyMalformedRangesCannotRun(t *testing.T) {
	p := carried(t, "a.go")
	if v := p.run(); v.State != 2 || !strings.Contains(said(v), "the carried ranges") {
		t.Fatalf("%s", said(v))
	}
	p = carried(t, "a.go:2-2")
	p.first(rule{"diff -M --unified=0", "fatal: bad object", 128, ""})
	if v := p.run(); v.State != 2 || !strings.Contains(said(v), "could not diff the carried ranges' base") {
		t.Fatalf("%s", said(v))
	}
}

// No real git, or no link for the helper: the run cannot hand gomutants its
// scope, and says so.
func TestNightlyAGitThatCannotBeMadeCannotRun(t *testing.T) {
	p := carried(t, "a.go:2-2")
	lookGit = func() (string, error) { return "", errors.New("not on PATH") }
	if v := p.run(); v.State != 2 || !strings.Contains(said(v), "the git that hands gomutants the carried ranges could not be made: not on PATH") {
		t.Fatalf("%s", said(v))
	}
	p = carried(t, "a.go:2-2")
	real := linkHelper
	linkHelper = func(dir, name string) (string, error) {
		if name == "git" {
			return "", errors.New("read-only")
		}
		return real(dir, name)
	}
	t.Cleanup(func() { linkHelper = real })
	if v := p.run(); v.State != 2 || !strings.Contains(said(v), "could not be made: read-only") {
		t.Fatalf("%s", said(v))
	}
}

// A carried file in another module is not this module's to grade.
func TestNightlyOnlyTheModulesOwnGoFilesAreCarried(t *testing.T) {
	p := newPull(t, "svc")
	t.Setenv(checks.EnvMutationRanges, "svc/a.go:2-2,other/b.go:1-1,svc/README.md:1-1")
	orig := lookGit
	t.Cleanup(func() { lookGit = orig })
	lookGit = func() (string, error) { return "/usr/bin/git", nil }
	p.first(rule{"git diff --relative", "", 0, ""})
	p.first(rule{"diff -M --unified=0", "", 0, ""})
	p.run()
	c, ok := p.call("-changed-since")
	if !ok || envOf(c, gitextra.EnvRanges) != "a.go:2-2" {
		t.Fatalf("gomutants' extra = %q (ran %v)", envOf(c, gitextra.EnvRanges), ok)
	}
}
