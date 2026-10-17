package tsmutation

import (
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/tsmut"
)

func TestTheDeclarationValidatesAndNamesItsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != tsmut.ID || Def.Stage != checks.StageMutation || Def.Lane != checks.LaneTS || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "bun" || Def.Surface != "root:package.json" || Def.NotOnForks != checks.ForkChangeBar ||
		Def.History != "full" || Def.WritesTree || len(Def.Needs) != 0 {
		t.Errorf("%+v", Def)
	}
	if tsmut.Row().ID != tsmut.ID || tsmut.Row().Stage != Def.Stage || tsmut.Row().Lane != Def.Lane {
		t.Errorf("the verdict's tsmut.Row() is not the declaration's: %+v", tsmut.Row())
	}
}
