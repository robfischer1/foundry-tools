package gomutation

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/gomut"
)

func TestTheDeclarationValidatesAndNamesItsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != gomut.ID || Def.Stage != checks.StageMutation || Def.Lane != checks.LaneGo || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "go" || Def.Surface != "any:**/go.mod" || Def.NotOnForks != checks.ForkChangeBar || Def.SubsumedBy != "" ||
		Def.History != "full" || Def.WritesTree || !Def.ReasksOwn ||
		strings.Join(Def.Needs, ",") != "dies,service:postgres,service:pgvector,service:redpanda,service:redis,tool:gomutants,tool:mutation-gate" {
		t.Errorf("%+v", Def)
	}
}
