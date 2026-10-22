package godiffcoverage

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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
//
// A `go test -coverprofile <p>` writes profile to p, as the suite does, when
// profile is set.
type fake struct {
	rules   []rule
	runs    []string
	profile string
}

func (f *fake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	f.runs = append(f.runs, c.Dir+": "+line)
	if i := slices.Index(c.Args, "-coverprofile"); c.Name == "go" && i >= 0 && i+1 < len(c.Args) && f.profile != "" {
		_ = os.WriteFile(c.Args[i+1], []byte(f.profile), 0o644)
	}
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
	t.Setenv("ATOM_OUT", "")
	t.Setenv("ATOM_TREE", "")
	old := atoms.GoCoverRoot
	atoms.GoCoverRoot = t.TempDir()
	t.Cleanup(func() { atoms.GoCoverRoot = old })

	f := &fake{profile: covProfile, rules: []rule{
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
		!slices.Equal(Def.AfterAny, []string{atoms.GoRaceRow.ID}) ||
		strings.Join(Def.Needs, ",") != "dies,"+strings.Join(checks.GravityNeeds(), ",")+",tool:wasm-tools" {
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
	// its own run writes under its own name, never over go:test-race's
	if !p.f.ran("go test -race -coverprofile "+atoms.GoCoverProfile(id, ".")+" ./...") || !p.f.ran(`go list -e -f {{.ImportPath}}{{"\t"}}{{.Dir}}{{"\n"}} ./...`) {
		t.Errorf("ran %v", p.f.runs)
	}
}

func TestGoDiffCoveragePassesWhenEveryChangedLineRan(t *testing.T) {
	p := newPull(t, ".")
	p.f.profile = strings.ReplaceAll(covProfile, " 0\n", " 2\n")
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
		{"no profile", func(p *pull) { p.f.profile = "" }, 2, false,
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

// raced is a pod in which go:test-race has settled as verdict: its verdict file
// beside this atom's ATOM_OUT and, when profile is set, its profile of the
// module at dir.
func (p *pull) raced(verdict checks.Verdict, profile string) {
	p.t.Helper()
	out := p.t.TempDir()
	p.t.Setenv("ATOM_OUT", filepath.Join(out, atomkit.EscapeID(id)+".json"))
	if err := atomkit.WriteFile(filepath.Join(out, atomkit.EscapeID(atoms.GoRaceRow.ID)+".json"), verdict); err != nil {
		p.t.Fatal(err)
	}
	if profile != "" {
		if err := os.WriteFile(atoms.GoCoverProfile(atoms.GoRaceRow.ID, p.where), []byte(profile), 0o644); err != nil {
			p.t.Fatal(err)
		}
	}
}

// WHERE go:test-race HAS SETTLED IN THIS POD, ITS PROFILE IS THE ONE READ: no
// second suite runs, no server is touched, and the reason says whose profile it
// was.
func TestGoDiffCoverageReadsTheProfileGoTestRaceLeftInThePod(t *testing.T) {
	for _, module := range []string{".", "tools/forge"} {
		t.Run(module, func(t *testing.T) {
			p := newPull(t, module)
			p.f.profile = "" // a suite run here would leave nothing to read
			p.raced(checks.Verdict{Atom: atoms.GoRaceRow.ID, State: 0, Result: "pass"}, covProfile)
			v := p.run()
			if v.State != 1 || !strings.Contains(v.Reason, "coverage: the profile of go:test-race's run in this pod") || len(v.Findings) != 1 {
				t.Fatalf("%+v", v)
			}
			if p.f.ran("go test") || p.f.ran("go mod download") {
				t.Errorf("ran a suite of its own: %v", p.f.runs)
			}
		})
	}
}

// A RED go:test-race STANDS THIS ATOM DOWN AS EXCLUDED, in every module at
// once: state 0 with result `excluded` and one `excluded` finding (the shape the
// runner writes itself, daedalus wire.Excluded), so the lane folds to the
// verdict of go:test-race. Never ABSENT (it was not absent), never could-not-run
// (it did not fail to run), never a suite of its own.
func TestGoDiffCoverageIsExcludedAfterARedGoTestRace(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verdict checks.Verdict
	}{
		{"findings", checks.Verdict{State: 1, Result: "findings"}},
		{"could not run", checks.Verdict{State: 2, Result: "cannot-run"}},
		{"itself excluded", checks.Verdict{State: 0, Result: "excluded"}},
	} {
		for _, module := range []string{".", "tools/forge"} {
			t.Run(tc.name+" in "+module, func(t *testing.T) {
				p := newPull(t, module)
				p.raced(tc.verdict, covProfile) // a profile left behind is not read
				v := p.run()
				if v.Atom != id || v.State != 0 || v.Result != "excluded" ||
					v.Reason != id+": EXCLUDED - go:test-race did not pass ("+tc.verdict.Result+"), so the profile this reads does not exist; go:test-race reports why" {
					t.Fatalf("%+v", v)
				}
				want := []checks.Finding{{Verdict: "excluded", Subject: id, Cause: "predecessor-red", Detail: "go:test-race did not pass", Probe: id}}
				if !slices.Equal(v.Findings, want) || v.Logs == nil || len(v.Logs) != 0 || v.Stage != checks.StagePrepush || v.Lane != string(checks.LaneGo) {
					t.Errorf("%+v", v)
				}
				if len(p.f.runs) != 0 {
					t.Errorf("asked something: %v", p.f.runs)
				}
			})
		}
	}
}

// WHERE go:test-race SETTLED WITHOUT A RED AND LEFT NO PROFILE (it passed and
// wrote none where this atom can see, or it stood down absent), the change set is
// measured again by a suite of this atom's own, never waved through.
func TestGoDiffCoverageMeasuresItselfWhenGoTestRaceLeftNoProfile(t *testing.T) {
	for _, verdict := range []checks.Verdict{{State: 0, Result: "pass"}, {State: 0, Result: "absent"}} {
		t.Run(verdict.Result, func(t *testing.T) {
			p := newPull(t, ".")
			p.raced(verdict, "")
			v := p.run()
			if v.State != 1 || !p.f.ran("go test -race") || !strings.Contains(v.Reason, "coverage: this atom's own run of the suite") {
				t.Errorf("%+v ran %v", v, p.f.runs)
			}
		})
	}
}

// A VERDICT THAT WILL NOT READ, or none yet, is go:test-race not settled: the
// atom measures the suite itself.
func TestGoDiffCoverageMeasuresItselfWithoutASettledGoTestRace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(p *pull)
	}{
		{"no verdict yet", func(p *pull) { p.t.Setenv("ATOM_OUT", filepath.Join(p.t.TempDir(), "x.json")) }},
		{"an unreadable verdict", func(p *pull) {
			out := p.t.TempDir()
			p.t.Setenv("ATOM_OUT", filepath.Join(out, "x.json"))
			if err := os.WriteFile(filepath.Join(out, atomkit.EscapeID(atoms.GoRaceRow.ID)+".json"), []byte("{"), 0o644); err != nil {
				p.t.Fatal(err)
			}
		}},
		{"an empty profile", func(p *pull) { p.raced(checks.Verdict{State: 0, Result: "pass"}, "") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPull(t, ".")
			tc.setup(p)
			if tc.name == "an empty profile" {
				if err := os.WriteFile(atoms.GoCoverProfile(atoms.GoRaceRow.ID, "."), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			v := p.run()
			if v.State != 1 || !p.f.ran("go test -race") || !strings.Contains(v.Reason, "coverage: this atom's own run of the suite") {
				t.Errorf("%+v ran %v", v, p.f.runs)
			}
		})
	}
}

// IN THE POD THIS ATOM HOLDS NO SERVERS: where go:test-race left no verdict, or
// passed and left no profile, it cannot run, and never runs the suite without
// the servers it would need.
func TestGoDiffCoverageCannotRunInThePodWithoutGoTestRacesProfile(t *testing.T) {
	for name, setup := range map[string]func(p *pull){
		"no verdict":                  func(p *pull) { p.t.Setenv("ATOM_OUT", filepath.Join(p.t.TempDir(), "x.json")) },
		"a pass that left no profile": func(p *pull) { p.raced(checks.Verdict{State: 0, Result: "pass"}, "") },
	} {
		t.Run(name, func(t *testing.T) {
			p := newPull(t, ".")
			setup(p)
			t.Setenv("ATOM_TREE", "/work/tree.json")
			v := p.run()
			if v.State != 2 || !strings.Contains(v.Reason, "go:diff-coverage: CANNOT RUN - go:test-race left no passing verdict and profile for this module in the pod") || p.f.ran("go test") {
				t.Errorf("%+v ran %v", v, p.f.runs)
			}
		})
	}
	// the profile there is read in the pod as anywhere
	p := newPull(t, ".")
	p.raced(checks.Verdict{State: 0, Result: "pass"}, covProfile)
	t.Setenv("ATOM_TREE", "/work/tree.json")
	if v := p.run(); v.State != 1 || !strings.Contains(v.Reason, "coverage: the profile of go:test-race's run in this pod") {
		t.Errorf("%+v", v)
	}
}
