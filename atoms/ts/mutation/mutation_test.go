package tsmutation

import (
	"strings"
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
		Def.History != "full" || Def.WritesTree || strings.Join(Def.Needs, ",") != "tool:git,tool:node" {
		t.Errorf("%+v", Def)
	}
}
