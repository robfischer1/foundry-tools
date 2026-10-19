package devtsregen

import (
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationValidatesAndIsTheWriteTwinOfTsRegen(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "dev:ts-regen" || Def.Stage != "dev" || Def.Lane != checks.LaneTS || Def.Toolchain != "rust-wasm" || Def.Surface != "root:package.json" ||
		!Def.WritesTree || Def.Timeout != 45*time.Minute || len(Def.Needs) != 0 || Def.Group != checks.GroupLanguage {
		t.Errorf("%+v", Def)
	}
}
