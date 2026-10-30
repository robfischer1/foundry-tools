package devpythonfmt

import (
	"context"
	"slices"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// The declaration is the dev verb of python:ruff-format, writing the tree, and
// the verb runs ruff's rewrite (never --check) under the fleet's ruleset.
func TestTheDeclarationAndTheRunAreTheRewriteOfTheRuffFormatTwin(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "dev:python-fmt" || Def.Stage != "dev" || Def.Lane != checks.LanePython || Def.Toolchain != "python" ||
		Def.Surface != "any:**/*.py" || !slices.Equal(Def.Needs, []string{"tool:ruff"}) || !Def.WritesTree {
		t.Errorf("%+v", Def)
	}
	t.Setenv("ATOM_WORK", t.TempDir())
	var ran []atoms.Cmd
	in := atomkit.In{Root: t.TempDir(), Exec: func(_ context.Context, c atoms.Cmd) (string, int) {
		ran = append(ran, c)
		return "", 0
	}}
	if v := Def.Check(t.Context(), in); v.State != int(checks.StatePass) || v.Atom != "dev:python-fmt" {
		t.Fatalf("%+v", v)
	}
	last := ran[len(ran)-1]
	if last.Name != "ruff" || last.Args[0] != "format" || slices.Contains(last.Args, "--check") {
		t.Errorf("ran %+v", last)
	}
}
