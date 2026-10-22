package fleetopengrepsast

import (
	"context"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// The declaration is the catalogue row it replaced: the same stage, group, text and
// fork lane, and the needs the split table gives it.
func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "none" || Def.NotOnForks != "" || Def.SubsumedBy != "" || Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
	if Def.Desc != "SAST scan that refuses a zero-file scan." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, []string{"tool:opengrep"}) {
		t.Errorf("needs %v", Def.Needs)
	}
}

// The atom runs internal/sastcheck's scan under its own row (the scan's cases are
// held there).
func TestTheAtomRunsTheSharedScanUnderItsOwnRow(t *testing.T) {
	in := atomkit.In{Root: t.TempDir(), Files: []string{"main.go"}}
	v := check(context.Background(), in)
	if v.Atom != "fleet:opengrep-sast" || v.Stage != checks.StagePrecommit || !strings.Contains(v.Reason+strings.Join(v.Logs, "\n"), "fleet:opengrep-sast") {
		t.Errorf("%+v", v)
	}
}
