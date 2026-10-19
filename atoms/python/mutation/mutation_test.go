package pythonmutation

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/pymut"
)

func TestTheDeclarationValidatesAndNamesItsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != pymut.ID || Def.Stage != checks.StageMutation || Def.Lane != checks.LanePython || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "python" || Def.Surface != "root:pyproject.toml" || Def.NotOnForks != checks.ForkChangeBar ||
		Def.History != "full" || !Def.NeedsManifest || Def.WritesTree || strings.Join(Def.Needs, ",") != "tool:git,tool:uv,tool:jq,tool:curl,tool:cosmic-ray" {
		t.Errorf("%+v", Def)
	}
}
