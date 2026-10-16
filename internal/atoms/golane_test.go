package atoms

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// golaneRule answers an exec whose argv line contains the needle.
type golaneRule struct {
	needle string
	out    string
	code   int
}

// golaneFake is an Exec that records every run (directory and argv line) and
// answers from its rules, first match wins; a run no rule matches exits 0.
type golaneFake struct {
	rules []golaneRule
	runs  []string
	envs  [][]string
}

func (f *golaneFake) exec(_ context.Context, c Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	f.runs = append(f.runs, c.Dir+": "+line)
	f.envs = append(f.envs, c.Env)
	for _, r := range f.rules {
		if strings.Contains(line, r.needle) {
			return r.out, r.code
		}
	}
	return "", 0
}

var golaneRow = checks.AtomDef{ID: "go:x", Stage: checks.StagePrepush, Lane: checks.LaneGo}

func TestEachGoModuleFoldsEveryModuleAndLeavesALoneRootUntouched(t *testing.T) {
	alone := Input{Root: "/src", Files: []string{"go.mod", "main.go"}}
	v := EachGoModule(alone, golaneRow, func(dir string) checks.Verdict {
		if dir != "." {
			t.Errorf("the root module is %q", dir)
		}
		return checks.VerdictOf(golaneRow, 0, "ok")
	})
	if v.Reason != "go:x: PASS" {
		t.Errorf("a repository whose only module is its root gets that verdict back untouched: %+v", v)
	}

	nested := Input{Root: "/src", Files: []string{"go.mod", "styx/go.mod", "vendor/y/go.mod", "styx/testdata/go.mod"}}
	var dirs []string
	v = EachGoModule(nested, golaneRow, func(dir string) checks.Verdict {
		dirs = append(dirs, dir)
		return checks.VerdictOf(golaneRow, 0, "ok")
	})
	if strings.Join(dirs, ",") != ".,styx" || !strings.Contains(v.Reason, "go:x: PASS in 2 Go modules (., styx)") {
		t.Errorf("a vendored or testdata module is not one: %v %+v", dirs, v)
	}

	v = EachGoModule(Input{Files: []string{"styx/go.mod"}}, golaneRow, func(dir string) checks.Verdict {
		return checks.VerdictOf(golaneRow, 1, "x.go:1: a finding")
	})
	if v.State != 1 || !strings.Contains(v.Reason, "FINDINGS in 1 of 1 Go module (styx)") || !strings.Contains(v.Reason, "── module styx ──") {
		t.Errorf("%+v", v)
	}
}

func TestEachGoModuleIsAbsentWithoutAModuleAndCannotRunWithoutATree(t *testing.T) {
	v := EachGoModule(Input{Files: []string{"README.md", "vendor/x/go.mod"}}, golaneRow, func(string) checks.Verdict {
		t.Error("an atom ran in a tree with no module")
		return checks.Verdict{}
	})
	if v.Result != "absent" || v.State != 0 {
		t.Errorf("%+v", v)
	}
	v = EachGoModule(Input{FilesErr: errors.New("boom")}, golaneRow, func(string) checks.Verdict { return checks.Verdict{} })
	if v.State != 2 || !strings.Contains(v.Reason, "go:x: CANNOT RUN - could not enumerate the tree's Go modules: boom") {
		t.Errorf("%+v", v)
	}
}

func TestGoModuleRootJoinsTheRootAndTheDirectory(t *testing.T) {
	if got := GoModuleRoot(Input{Root: "/src"}, "tools/forge"); got != "/src/tools/forge" {
		t.Errorf("got %q", got)
	}
	if got := GoModuleRoot(Input{Root: "/src"}, "."); got != "/src" {
		t.Errorf("got %q", got)
	}
}

// A provisioning step under the default Expect: its failure is the atom's
// could-not-run, worded with the step and its last line.
func TestProvisionIsACouldNotRunWhenTheStepFails(t *testing.T) {
	f := &golaneFake{}
	if _, ok := Provision(t.Context(), Input{Exec: f.exec}, golaneRow, "/src", "go", "mod", "download"); !ok {
		t.Fatal("a step that exited 0 failed")
	}
	if f.runs[0] != "/src: go mod download" {
		t.Errorf("ran %v", f.runs)
	}
	f = &golaneFake{rules: []golaneRule{{"mod download", "go: fetching\nproxy 502\n", 1}}}
	v, ok := Provision(t.Context(), Input{Exec: f.exec}, golaneRow, "/src", "go", "mod", "download")
	if ok || v.State != 2 || !strings.Contains(v.Reason, "go:x: CANNOT RUN - preflight `go mod download` exited 1: proxy 502") {
		t.Errorf("%v %+v", ok, v)
	}
}

func TestNeverRanAndTheLastLine(t *testing.T) {
	v := NeverRan(golaneRow, "exec: \"go\": not found")
	if v.State != 2 || !strings.Contains(v.Reason, "the atom never ran: exec: \"go\": not found") {
		t.Errorf("%+v", v)
	}
	if LastLine("a\nb\n") != "b" || LastLine("") != "" {
		t.Error("the last line is the last line")
	}
}

func TestChangeBaseIsTheMergeBaseOrNothingOrAnError(t *testing.T) {
	in := func(f *golaneFake) Input { return Input{Base: "abc123", Exec: f.exec} }

	f := &golaneFake{rules: []golaneRule{{"merge-base", "since0\n", 0}}}
	since, err := ChangeBase(t.Context(), in(f), "/src/x")
	if since != "since0" || err != nil {
		t.Errorf("%q %v", since, err)
	}
	if f.runs[0] != "/src/x: git rev-parse --verify --quiet abc123^{commit}" || f.runs[1] != "/src/x: git merge-base abc123 HEAD" {
		t.Errorf("ran %v", f.runs)
	}

	// a base the history does not reach is no error and no commit
	f = &golaneFake{rules: []golaneRule{{"rev-parse", "", 1}}}
	if since, err = ChangeBase(t.Context(), in(f), "/src"); since != "" || err != nil {
		t.Errorf("%q %v", since, err)
	}

	for name, rules := range map[string][]golaneRule{
		"git would not start":        {{"rev-parse", "git: not found", -1}},
		"merge-base would not start": {{"merge-base", "git: not found", -1}},
	} {
		if _, err = ChangeBase(t.Context(), in(&golaneFake{rules: rules}), "/src"); err == nil || !strings.Contains(err.Error(), "the atom never ran: git: not found") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// unrelated histories, and an answer of nothing
	for name, rule := range map[string]golaneRule{"exit": {"merge-base", "fatal: no merge base", 1}, "empty": {"merge-base", "\n", 0}} {
		if _, err = ChangeBase(t.Context(), in(&golaneFake{rules: []golaneRule{rule}}), "/src"); err == nil || !strings.Contains(err.Error(), "git found no merge base between the base abc123 and HEAD") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// ON A TREE THE DOOR HAD FETCHED a base it cannot reach is a could-not-run; a
// local snapshot stands down under the atom's own words.
func TestMissingBaseCannotRunOnTheDoorsTreeAndStandsDownLocally(t *testing.T) {
	old := doorTree
	t.Cleanup(func() { doorTree = old })

	doorTree = func() bool { return true }
	state, why := MissingBase(Input{Base: "abc123"}, "ABSENT - no base")
	if state != 2 || !strings.Contains(why, "CANNOT RUN - the base abc123 is not in this history, though the door named it") {
		t.Errorf("%d %q", state, why)
	}
	doorTree = func() bool { return false }
	if state, why = MissingBase(Input{Base: "abc123"}, "ABSENT - no base"); state != 0 || why != "ABSENT - no base" {
		t.Errorf("%d %q", state, why)
	}
}

func TestTheDoorTreeIsTheOneThatNamedItsOrigin(t *testing.T) {
	t.Setenv("ATOM_ORIGIN", "")
	if doorTree() {
		t.Error("no origin named, so no door tree")
	}
	t.Setenv("ATOM_ORIGIN", "https://door/x")
	if !doorTree() {
		t.Error("an origin named is the door's tree")
	}
}

var errTreeGone = errors.New("gone")
