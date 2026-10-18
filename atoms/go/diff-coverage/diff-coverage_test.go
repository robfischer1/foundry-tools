package godiffcoverage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE CHAIN OF THE go:diff-coverage ATOM. The judgement — which lines are
// uncovered — is internal/checks' (godiffcoverage_test.go); these pin what the
// atom asks, in what order, and what it does with each answer.

const (
	covDiff = "diff --git a/f.go b/f.go\n--- /dev/null\n+++ b/f.go\n@@ -0,0 +1,8 @@\n"
	covSrc  = "package x\n\nfunc F(x int) int {\n\tif x > 0 {\n\t\treturn 1\n\t}\n\treturn 0\n}\n"
	// covProfile: F ran with x <= 0, so the if-body on line 5 never did.
	covProfile = "mode: atomic\nx/f.go:3.19,4.11 1 1\nx/f.go:4.11,6.3 1 0\nx/f.go:7.2,7.10 1 1\n"
)

// rule answers an exec whose argv line contains the needle.
type rule struct {
	needle string
	out    string
	code   int
}

// fake is an Exec that records every run and answers from its rules, first
// match wins; a run no rule matches exits 0 saying nothing.
type fake struct {
	rules []rule
	runs  []string
}

func (f *fake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	f.runs = append(f.runs, c.Dir+": "+line)
	for _, r := range f.rules {
		if strings.Contains(line, r.needle) {
			return r.out, r.code
		}
	}
	return "", 0
}

// ran says whether any run's argv line contains the needle.
func (f *fake) ran(needle string) bool {
	for _, r := range f.runs {
		if strings.Contains(r, needle) {
			return true
		}
	}
	return false
}

// pull is a module on disk, its suite's profile where the atom reads it, and an
// Exec that answers a pull which added f.go, whose line 5 no test runs.
type pull struct {
	t     *testing.T
	in    atomkit.In
	f     *fake
	dir   string
	where string // the module's directory in the tree
}

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newPull(t *testing.T, module string) *pull {
	t.Helper()
	t.Setenv("ATOM_ORIGIN", "")
	root := t.TempDir()
	mod := filepath.Join(root, module)
	put(t, root, filepath.Join(module, "go.mod"), "module x\n\ngo 1.26\n")
	put(t, root, filepath.Join(module, "f.go"), covSrc)
	put(t, root, filepath.Join(module, "f_test.go"), "package x\n")
	profile := filepath.Join(t.TempDir(), "cover.out")
	if err := os.WriteFile(profile, []byte(covProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	old := profilePath
	profilePath = profile
	t.Cleanup(func() { profilePath = old })

	f := &fake{rules: []rule{
		{"merge-base", "since0\n", 0},
		{"git diff --unified=0", covDiff, 0},
		{"go list -f {{len", "11\n", 0},
		{"go list -e -f", "x\t" + mod + "\n", 0},
	}}
	files := []string{filepath.Join(module, "go.mod"), filepath.Join(module, "f.go"), filepath.Join(module, "f_test.go")}
	return &pull{t: t, f: f, dir: mod, where: module,
		in: atomkit.In{Root: root, Files: files, Base: "abc123", Exec: f.exec}}
}

// first puts a rule in front of the others.
func (p *pull) first(r rule) { p.f.rules = append([]rule{r}, p.f.rules...) }

func (p *pull) run() checks.Verdict { return check(p.t.Context(), p.in) }

func TestTheDeclarationValidatesAndNamesItsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneGo || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "go" || Def.Surface != "any:**/go.mod" || Def.NotOnForks != checks.ForkChangeBar || Def.SubsumedBy != "" ||
		strings.Join(Def.Needs, ",") != "dies,service:postgres,service:pgvector,service:redpanda,service:redis,tool:gravity" {
		t.Errorf("%+v", Def)
	}
}

func TestGoDiffCoverageNamesEachChangedLineNoTestRuns(t *testing.T) {
	p := newPull(t, ".")
	v := p.run()
	if v.State != 1 || !strings.Contains(v.Reason, "1 changed line(s) no test executes") || !strings.Contains(v.Reason, "  f.go:5") {
		t.Fatalf("%+v", v)
	}
	if len(v.Findings) != 1 || v.Findings[0].Subject != "f.go:5" || v.Findings[0].Cause != "not-covered" || v.Findings[0].Verdict != "violated" {
		t.Errorf("findings: %+v", v.Findings)
	}
	// the change set is asked first, in the module, against the merge base; the
	// suite runs the race run and the packages are listed to place the profile
	want := []string{
		": git rev-parse --verify --quiet abc123^{commit}",
		": git merge-base abc123 HEAD",
		": git diff --unified=0 --no-color --no-ext-diff --relative since0 -- *.go",
		": go mod download",
		": go list -f {{len .TestGoFiles}}{{len .XTestGoFiles}} ./...",
	}
	for i, w := range want {
		if !strings.HasSuffix(p.f.runs[i], w) {
			t.Errorf("run %d is %q, want ...%s", i, p.f.runs[i], w)
		}
	}
	if !p.f.ran("go test -race -coverprofile "+checks.GoCoverProfile+" ./...") || !p.f.ran(`go list -e -f {{.ImportPath}}{{"\t"}}{{.Dir}}{{"\n"}} ./...`) {
		t.Errorf("ran %v", p.f.runs)
	}
}

func TestGoDiffCoveragePassesWhenEveryChangedLineRan(t *testing.T) {
	p := newPull(t, ".")
	if err := os.WriteFile(profilePath, []byte(strings.ReplaceAll(covProfile, " 0\n", " 2\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	v := p.run()
	if v.State != 0 || v.Result != "pass" || v.Findings != nil || !strings.Contains(v.Reason, "go:diff-coverage: PASS") ||
		strings.Join(v.Logs, "\n") != "go:diff-coverage: every changed line in 1 Go file(s) ran under the suite" {
		t.Errorf("a clean pull: %+v", v)
	}
}

// Every way the atom stands down or cannot run, and that it asks for nothing
// past the point that decided it.
func TestGoDiffCoverageStandsDownOrCannotRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script func(p *pull)
		state  int
		absent bool
		want   string
		never  string // a run the atom must not have got to
	}{
		{"no base", func(p *pull) { p.in.Base = "" }, 0, true, "ABSENT - no usable PR base sha", "git diff"},
		{"a base the history does not reach, locally", func(p *pull) { p.first(rule{"rev-parse", "", 1}) }, 0, true,
			"ABSENT - no usable PR base sha", "git diff"},
		{"a base the door's tree does not reach", func(p *pull) {
			p.first(rule{"rev-parse", "", 1})
			p.t.Setenv("ATOM_ORIGIN", "https://door/x")
		}, 2, false, "CANNOT RUN - the base abc123 is not in this history", "git diff"},
		{"the base cannot be resolved", func(p *pull) { p.first(rule{"rev-parse", "git: not found", -1}) }, 2, false,
			"CANNOT RUN - the atom never ran: git: not found", "git diff"},
		{"git cannot diff", func(p *pull) { p.first(rule{"git diff", "bad object", 2}) }, 2, false,
			"CANNOT RUN - the pull could not be diffed against its base since0: git diff exited 2\nbad object", "go test"},
		{"the diff never ran", func(p *pull) { p.first(rule{"git diff", "lost", -1}) }, 2, false,
			"CANNOT RUN - the pull could not be diffed against its base since0: git diff never ran: lost", "go test"},
		{"no judged Go changed", func(p *pull) {
			p.first(rule{"git diff", "--- a/f_test.go\n+++ b/f_test.go\n@@ -1 +1 @@\n", 0})
		}, 0, true, "ABSENT - this pull adds no line to a non-test, non-generated Go file of this module", "go test"},
		{"the suite failed", func(p *pull) { p.first(rule{"go test", "FAIL", 1}) }, 0, true,
			"ABSENT - go:test-race did not pass (findings)", "go list -e"},
		{"the suite could not run", func(p *pull) { p.first(rule{"go list -f", "go: not found", -1}) }, 0, true,
			"ABSENT - go:test-race did not pass (cannot-run)", "go test"},
		{"no profile", func(p *pull) { _ = os.Remove(profilePath) }, 2, false,
			"CANNOT RUN - the suite passed and its coverage profile could not be read: ", "go list -e"},
		{"the packages cannot be listed", func(p *pull) { p.first(rule{"go list -e", "", 1}) }, 2, false,
			"CANNOT RUN - the module's packages could not be listed, so the profile cannot be placed: go list exited 1", ""},
		{"the packages listing never ran", func(p *pull) { p.first(rule{"go list -e", "go: not found", -1}) }, 2, false,
			"CANNOT RUN - the module's packages could not be listed, so the profile cannot be placed: go list never ran: go: not found", ""},
		{"a changed file the tree lacks", func(p *pull) {
			p.first(rule{"git diff", "--- /dev/null\n+++ b/g.go\n@@ -0,0 +1 @@\n", 0})
		}, 2, false, "CANNOT RUN - a changed file could not be read from the tree: g.go", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPull(t, ".")
			tc.script(p)
			v := p.run()
			if v.State != tc.state {
				t.Fatalf("state %d (%s), want %d:\n%s", v.State, v.Result, tc.state, v.Reason)
			}
			if got := v.Reason + "\n" + strings.Join(v.Logs, "\n"); !strings.Contains(got, "go:diff-coverage: "+tc.want) {
				t.Errorf("the verdict lacks %q:\n%s", tc.want, got)
			}
			if (v.Result == "absent") != tc.absent || v.Findings != nil {
				t.Errorf("result %s, findings %v", v.Result, v.Findings)
			}
			if tc.never != "" && p.f.ran(tc.never) {
				t.Errorf("went on to %s: %v", tc.never, p.f.runs)
			}
		})
	}
}

// A NESTED MODULE IS HELD AGAINST ITS OWN SUITE, its findings reach the folded
// verdict (a fold keeps states and reasons, not findings), and their subjects
// are named from the repository root.
func TestGoDiffCoverageInANestedModuleKeepsItsFindings(t *testing.T) {
	p := newPull(t, "tools/forge")
	v := p.run()
	if v.State != 1 || !strings.Contains(v.Reason, "FINDINGS in 1 of 1 Go module (tools/forge)") || !strings.Contains(v.Reason, "  tools/forge/f.go:5") {
		t.Fatalf("%+v", v)
	}
	if len(v.Findings) != 1 || v.Findings[0].Subject != "tools/forge/f.go:5" {
		t.Errorf("findings: %+v", v.Findings)
	}
	for _, r := range p.f.runs {
		if !strings.HasPrefix(r, p.dir+":") {
			t.Errorf("%q ran outside the module's directory %s", r, p.dir)
		}
	}
}
