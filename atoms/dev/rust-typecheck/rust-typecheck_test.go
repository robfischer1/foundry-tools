package devrusttypecheck

import (
	"context"
	"slices"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
)

// RUSTDEV-8: the atom is the dev verb `typecheck` over the shared body, and a bare
// run in a tree with no scope script is the whole workspace, as before.
func TestRUSTDEV_8_TheAtomRunsTheSharedBody(t *testing.T) {
	if Def.ID != "dev:rust-typecheck" || Def.Stage != "dev" || Def.Surface != "root:Cargo.toml" || Def.WritesTree {
		t.Fatalf("%+v", Def)
	}
	var ran [][]string
	in := atomkit.In{Root: t.TempDir(), Exec: func(_ context.Context, c atoms.Cmd) (string, int) {
		ran = append(ran, append([]string{c.Name}, c.Args...))
		return "", 0
	}}
	if v := Def.Check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	if want := []string{"cargo", "check", "--workspace"}; len(ran) != 2 || !slices.Equal(ran[1], want) {
		t.Errorf("ran %q, want %q last", ran, want)
	}
}
