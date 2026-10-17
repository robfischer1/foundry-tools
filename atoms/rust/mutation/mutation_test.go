package rustmutation

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/rustmut"
)

func TestTheDeclarationValidatesAndNamesItsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != rustmut.ID || Def.Stage != checks.StageMutation || Def.Lane != checks.LaneRust || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "rust-trixie" || Def.Surface != "root:Cargo.toml" || Def.NotOnForks != checks.ForkChangeBar ||
		Def.History != "full" || Def.WritesTree || strings.Join(Def.Needs, ",") != "tool:cargo-mutants,tool:cargo-nextest" {
		t.Errorf("%+v", Def)
	}
}
