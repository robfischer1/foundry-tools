package pythonmypy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/rulesets"
)

type script struct {
	calls []atoms.Cmd
	exits []int
	out   []string
}

func (s *script) exec(_ context.Context, c atoms.Cmd) (string, int) {
	s.calls = append(s.calls, c)
	i := len(s.calls) - 1
	if i >= len(s.exits) {
		return "", 0
	}
	return s.out[i], s.exits[i]
}

// tree lays the directories named under a temp root and answers the input over
// them: dirs are the root's entries, files the population.
func tree(t *testing.T, s *script, dirs []string, files ...string) atomkit.In {
	t.Helper()
	t.Setenv("ATOM_WORK", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return atomkit.In{Root: root, Files: files, Exec: s.exec}
}

func TestTheDeclarationValidatesAndKeepsTheRowItReplaces(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LanePython || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "python" || Def.NotOnForks != checks.ForkFleetStyle || !Def.NeedsManifest || !Def.WritesTree || Def.Surface != "root:pyproject.toml" ||
		Def.Desc != "mypy is clean over src and tests, under the fleet's strict configuration." ||
		!slices.Equal(Def.Needs, []string{"tool:uv"}) || Def.Timeout != 30*time.Minute {
		t.Errorf("%+v", Def)
	}
}

func TestMypyTypeChecksTheProductDirsUnderTheFleetsConfig(t *testing.T) {
	s := &script{}
	in := tree(t, s, []string{"src", "tests"}, "src/x.py", "tests/test_x.py")
	if v := check(context.Background(), in); v.State != 0 || v.Reason != "python:mypy: PASS" {
		t.Fatalf("%+v", v)
	}
	if c := s.calls[0]; c.Name != "uv" || !slices.Equal(c.Args, []string{"--version"}) {
		t.Errorf("the probe: %+v", c)
	}
	cfg := filepath.Join(os.Getenv("ATOM_WORK"), "mypy.ini")
	c := s.calls[1]
	if want := []string{"run", "--all-extras", "mypy", "--config-file", cfg, "src", "tests"}; c.Name != "uv" || c.Dir != in.Root || !c.Both || !slices.Equal(c.Args, want) {
		t.Errorf("%+v", c)
	}
	// pyright is deliberately absent from this lane; one type checker, not two.
	if strings.Contains(strings.Join(c.Args, " "), "pyright") {
		t.Errorf("one type checker: %v", c.Args)
	}
	if got, err := os.ReadFile(cfg); err != nil || string(got) != rulesets.Mypy {
		t.Errorf("the config mypy reads is not the embedded ruleset: %v", err)
	}

	// one product dir is still a target list
	s = &script{}
	in = tree(t, s, []string{"src"}, "src/x.py")
	check(context.Background(), in)
	if got := s.calls[1].Args; got[len(got)-1] != "src" || slices.Contains(got, "tests") {
		t.Errorf("a tree with only src/ types src/ alone: %v", got)
	}
}

// A GO STAR'S src/ IS NOT PYTHON, and mypy pointed at one answers exit 2.
func TestMypySkipsProductDirsThatHoldNoPython(t *testing.T) {
	s := &script{}
	in := tree(t, s, []string{"src", "tests"}, "src/main.go", "tests/x_test.go", "scripts/one.py")
	v := check(context.Background(), in)
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "python:mypy: ABSENT - no src/ or tests/ carrying python to type-check") || len(s.calls) != 0 {
		t.Errorf("%+v %v", v, s.calls)
	}
}

func TestMypyReadsItsExitAndTheEnumerationAndTheRoot(t *testing.T) {
	s := &script{exits: []int{0, 1}, out: []string{"", "src/x.py:3: error: Function is missing a return type annotation"}}
	in := tree(t, s, []string{"src"}, "src/x.py")
	if v := check(context.Background(), in); v.State != 1 || !strings.Contains(v.Reason, "missing a return type annotation") {
		t.Errorf("%+v", v)
	}
	s = &script{exits: []int{0, 2}, out: []string{"", ""}}
	in = tree(t, s, []string{"src"}, "src/x.py")
	if v := check(context.Background(), in); v.State != 2 {
		t.Errorf("%+v", v)
	}
	s = &script{exits: []int{-1}, out: []string{"uv: executable file not found"}}
	in = tree(t, s, []string{"src"}, "src/x.py")
	if v := check(context.Background(), in); v.State != 2 || !strings.Contains(v.Reason, "the atom never ran") {
		t.Errorf("%+v", v)
	}

	s = &script{}
	in = tree(t, s, []string{"src"}, "src/x.py")
	in.FilesErr = errors.New("git went away")
	if v := check(context.Background(), in); v.State != 2 || !strings.Contains(v.Reason, "python:mypy: CANNOT RUN - could not enumerate the repository: git went away") || len(s.calls) != 0 {
		t.Errorf("%+v", v)
	}
	in = atomkit.In{Root: filepath.Join(t.TempDir(), "absent"), Exec: s.exec}
	if v := check(context.Background(), in); v.State != 2 || !strings.Contains(v.Reason, "could not read the repository root") {
		t.Errorf("%+v", v)
	}
}

func TestARulesetThatCannotBeWrittenIsACouldNotRun(t *testing.T) {
	s := &script{}
	in := tree(t, s, []string{"src"}, "src/x.py")
	t.Setenv("ATOM_WORK", filepath.Join(in.Root, "pyproject.toml", "work"))
	if v := check(context.Background(), in); v.State != 2 || !strings.Contains(v.Reason, "the fleet's ruleset could not be written") || len(s.calls) != 0 {
		t.Errorf("%+v %v", v, s.calls)
	}
}
