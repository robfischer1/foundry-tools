package gogofmt

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationValidatesAndNamesItsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Group != checks.GroupOf(id) || Def.Toolchain != "go" || Def.Lane != checks.LaneGo || Def.NotOnForks != checks.ForkFleetStyle {
		t.Errorf("%+v", Def)
	}
}

// The atom asks internal/gofmtcheck's question under its own row (the question's
// cases are held there).
func TestTheAtomAsksTheSharedQuestionUnderItsOwnRow(t *testing.T) {
	exec := func(context.Context, atoms.Cmd) (string, int) { return "main.go", 0 }
	v := check(context.Background(), atomkit.In{Root: "/src", Files: []string{"main.go"}, Exec: exec})
	if v.Atom != "go:gofmt" || v.Stage != checks.StagePrecommit || v.State != 1 || !strings.Contains(v.Reason, "go:gofmt: FINDINGS (exit 1)") {
		t.Errorf("%+v", v)
	}
}
