package pythonruffformat

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

// tree lays files under a temp root and answers the atom's input over it.
func tree(t *testing.T, s *script, files map[string]string) atomkit.In {
	t.Helper()
	t.Setenv("ATOM_WORK", t.TempDir())
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return atomkit.In{Root: dir, Exec: s.exec}
}

// argv is what the format run was handed, after the program name.
func (s *script) argv(t *testing.T) []string {
	t.Helper()
	if len(s.calls) != 2 {
		t.Fatalf("want the probe and the format, got %v", s.calls)
	}
	return s.calls[1].Args
}

func TestTheDeclarationValidatesAndKeepsTheRowItReplaces(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LanePython || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "python" || Def.NotOnForks != checks.ForkFleetStyle || Def.NeedsManifest ||
		Def.Desc != "ruff format --check is clean over the product python, under the fleet's ruleset." ||
		!slices.Equal(Def.Needs, []string{"tool:ruff"}) {
		t.Errorf("%+v", Def)
	}
}

// A wheel builder is graded at the fleet's one width, 100, like every tree.
func TestRuffFormatGradesAWheelBuilderAtTheFleetWidth(t *testing.T) {
	s := &script{}
	in := tree(t, s, map[string]string{"pyproject.toml": "[project]\nname = \"x\"\n\n[build-system]\nrequires = [\"hatchling\"]\n", "src/x.py": ""})
	if v := check(context.Background(), in); v.State != 0 || v.Reason != "python:ruff-format: PASS" {
		t.Fatalf("%+v", v)
	}
	cfg := filepath.Join(os.Getenv("ATOM_WORK"), "ruff.toml")
	if got := s.argv(t); !slices.Equal(got, []string{"format", "--config", cfg, "--line-length", "100", "--check", "."}) {
		t.Errorf("%v", got)
	}
	if got, err := os.ReadFile(cfg); err != nil || string(got) != rulesets.Ruff {
		t.Errorf("the config ruff reads is not the embedded ruleset: %v", err)
	}
	if c := s.calls[0]; c.Name != "ruff" || !slices.Equal(c.Args, []string{"--version"}) {
		t.Errorf("the probe: %+v", c)
	}
}

// A go star with product python: src and tests, never the whole tree, at the
// fleet's one width.
func TestRuffFormatScopesAGoStarToItsProductPython(t *testing.T) {
	s := &script{}
	in := tree(t, s, map[string]string{"go.mod": "module x\n", "pyproject.toml": "[project]\nname = \"x\"\n", "src/x.py": "", "tests/test_x.py": ""})
	if v := check(context.Background(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	got := s.argv(t)
	if !slices.Equal(got[3:], []string{"--line-length", "100", "--check", "src", "tests"}) {
		t.Errorf("%v", got)
	}
	// --check, NEVER the rewrite
	if !slices.Contains(got, "--check") {
		t.Errorf("ruff format must never run without --check: %v", got)
	}

	// A go star with neither src/ nor tests/ has nothing to format, and says so.
	s = &script{}
	in = tree(t, s, map[string]string{"go.mod": "module x\n", "scripts/one.py": ""})
	v := check(context.Background(), in)
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "python:ruff-format: ABSENT - a go star with no product python under src/ or tests/") || len(s.calls) != 0 {
		t.Errorf("%+v %v", v, s.calls)
	}
}

func TestRuffFormatReadsRuffsExitAndNeverRunsWithoutItsTool(t *testing.T) {
	s := &script{exits: []int{0, 1}, out: []string{"", "Would reformat: src/x.py"}}
	in := tree(t, s, map[string]string{"src/x.py": ""})
	if v := check(context.Background(), in); v.State != 1 || !strings.Contains(v.Reason, "Would reformat") {
		t.Errorf("%+v", v)
	}
	s = &script{exits: []int{127}, out: []string{"not found"}}
	in = tree(t, s, map[string]string{"src/x.py": ""})
	if v := check(context.Background(), in); v.State != 2 || !strings.Contains(v.Reason, "the atom never ran") || len(s.calls) != 1 {
		t.Errorf("%+v", v)
	}
}

func TestATreeThatWillNotListIsACouldNotRun(t *testing.T) {
	s := &script{}
	in := atomkit.In{Root: filepath.Join(t.TempDir(), "absent"), Exec: s.exec}
	v := check(context.Background(), in)
	if v.State != 2 || !strings.Contains(v.Reason, "python:ruff-format: CANNOT RUN - could not read the repository root") || len(s.calls) != 0 {
		t.Errorf("%+v", v)
	}
}

func TestARulesetThatCannotBeWrittenIsACouldNotRun(t *testing.T) {
	s := &script{}
	in := tree(t, s, map[string]string{"src/x.py": ""})
	file := filepath.Join(in.Root, "src", "x.py")
	t.Setenv("ATOM_WORK", filepath.Join(file, "work"))
	v := check(context.Background(), in)
	if v.State != 2 || !strings.Contains(v.Reason, "the fleet's ruleset could not be written") || len(s.calls) != 0 {
		t.Errorf("%+v %v", v, s.calls)
	}
}
