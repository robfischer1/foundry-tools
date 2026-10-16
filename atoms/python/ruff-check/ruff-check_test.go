package pythonruffcheck

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

// script records each program the atom runs and answers from a list, then with
// success.
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

func inWith(s *script) atomkit.In {
	return atomkit.In{Root: "/src", Files: []string{"src/x.py"}, Exec: s.exec}
}

func TestTheDeclarationValidatesAndKeepsTheRowItReplaces(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LanePython || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "python" || Def.NotOnForks != checks.ForkFleetStyle || Def.NeedsManifest ||
		Def.Desc != "ruff lint is clean over every .py in the tree, under the fleet's ruleset." ||
		!slices.Equal(Def.Needs, []string{"tool:ruff"}) {
		t.Errorf("%+v", Def)
	}
}

func TestRuffChecksTheTreeUnderTheFleetsRulesetAndReadsTheExit(t *testing.T) {
	t.Setenv("ATOM_WORK", t.TempDir())
	s := &script{}
	v := check(context.Background(), inWith(s))
	if v.State != 0 || v.Reason != "python:ruff-check: PASS" || len(s.calls) != 2 {
		t.Fatalf("%+v %v", v, s.calls)
	}
	if c := s.calls[0]; c.Name != "ruff" || !slices.Equal(c.Args, []string{"--version"}) {
		t.Errorf("the provisioning probe comes first: %+v", c)
	}
	c := s.calls[1]
	cfg := filepath.Join(os.Getenv("ATOM_WORK"), "ruff.toml")
	if c.Name != "ruff" || c.Dir != "/src" || !c.Both || !slices.Equal(c.Args, []string{"check", "--config", cfg, "."}) {
		t.Errorf("%+v", c)
	}
	// the ruleset the tool is pointed at is the fleet's, written before it ran
	if got, err := os.ReadFile(cfg); err != nil || string(got) != rulesets.Ruff {
		t.Errorf("the config ruff reads is not the embedded ruleset: %v", err)
	}

	s = &script{exits: []int{0, 1}, out: []string{"", "src/x.py:1:1: F401 [*] `os` imported but unused"}}
	if v := check(context.Background(), inWith(s)); v.State != 1 || !strings.Contains(v.Reason, "F401") {
		t.Errorf("ruff's exit is the verdict: %+v", v)
	}
	s = &script{exits: []int{0, 2}, out: []string{"", "ruff failed: invalid configuration"}}
	if v := check(context.Background(), inWith(s)); v.State != 2 || !strings.Contains(v.Reason, "invalid configuration") {
		t.Errorf("%+v", v)
	}
}

func TestAProbeThatFailsIsTheAtomNeverRunning(t *testing.T) {
	t.Setenv("ATOM_WORK", t.TempDir())
	s := &script{exits: []int{-1}, out: []string{"ruff: executable file not found in $PATH"}}
	v := check(context.Background(), inWith(s))
	if v.State != 2 || !strings.Contains(v.Reason, "the atom never ran") || len(s.calls) != 1 {
		t.Errorf("%+v %v", v, s.calls)
	}
}

func TestARulesetThatCannotBeWrittenIsACouldNotRun(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_WORK", filepath.Join(file, "work"))
	s := &script{}
	v := check(context.Background(), inWith(s))
	if v.State != 2 || !strings.Contains(v.Reason, "python:ruff-check: CANNOT RUN - the fleet's ruleset could not be written") || len(s.calls) != 0 {
		t.Errorf("%+v %v", v, s.calls)
	}
}
