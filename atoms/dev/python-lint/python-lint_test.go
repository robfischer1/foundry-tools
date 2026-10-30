package devpythonlint

import (
	"context"
	"slices"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// The declaration is the dev verb of python:ruff-check: its toolchain, lane,
// surface and tool, and the verb lays the fleet's ruleset on ruff's cache dir.
func TestTheDeclarationAndTheRunAreThoseOfTheRuffCheckTwin(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "dev:python-lint" || Def.Stage != "dev" || Def.Lane != checks.LanePython || Def.Toolchain != "python" ||
		Def.Surface != "any:**/*.py" || !slices.Equal(Def.Needs, []string{"tool:ruff"}) || Def.WritesTree {
		t.Errorf("%+v", Def)
	}
	t.Setenv("ATOM_WORK", t.TempDir())
	var ran []atoms.Cmd
	in := atomkit.In{Root: t.TempDir(), Args: []string{"src"}, Exec: func(_ context.Context, c atoms.Cmd) (string, int) {
		ran = append(ran, c)
		return "", 0
	}}
	if v := Def.Check(t.Context(), in); v.State != int(checks.StatePass) || v.Atom != "dev:python-lint" {
		t.Fatalf("%+v", v)
	}
	last := ran[len(ran)-1]
	if last.Name != "ruff" || last.Args[0] != "check" || !slices.Contains(last.Env, "RUFF_CACHE_DIR="+ruffCache) || last.Args[len(last.Args)-1] != "src" {
		t.Errorf("ran %+v", last)
	}
}
