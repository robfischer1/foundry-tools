package tsregen

import (
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationValidatesAndKeepsTheRowItReplaces(t *testing.T) {
	const id = "ts:regen"
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneTS || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "rust-wasm" || Def.NotOnForks != "" || !Def.NeedsManifest || Def.Surface != "root:package.json" || Def.WritesTree ||
		Def.Timeout != 45*time.Minute || len(Def.Needs) != 0 ||
		!strings.HasPrefix(Def.Desc, "Each core the justfile's regen-check names regenerates byte-identical") {
		t.Errorf("%+v", Def)
	}
}
