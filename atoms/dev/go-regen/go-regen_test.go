package devgoregen

import (
	"slices"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationValidatesAndHasTheShapeTheVerbNeeds(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "dev:go-regen" || Def.Stage != "dev" || Def.Lane != checks.LaneGo || Def.Toolchain != "rust" || Def.Surface != "root:go.mod" ||
		!Def.WritesTree || Def.Timeout != 45*time.Minute || Def.Group != checks.GroupLanguage {
		t.Errorf("%+v", Def)
	}
	for _, need := range append([]string{"tool:wasm-tools", "tool:wac", "tool:gofmt"}, checks.GravityNeeds()...) {
		if !slices.Contains(Def.Needs, need) {
			t.Errorf("does not need %s: %v", need, Def.Needs)
		}
	}
	if len(Def.Needs) != 3+len(checks.GravityPrebuilt) {
		t.Errorf("needs %v", Def.Needs)
	}
}
