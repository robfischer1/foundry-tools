package bundleresign

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/bundlekit"
	"dagger/foundry-tools/internal/checks"
)

// The declaration is the bundle lane's alone: its own stage, and the opa it needs.
func TestTheDeclaration(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StageBundle || Def.Lane != checks.LaneAny || Def.Toolchain != "none" ||
		Def.Surface != "all:policy/.manifest,fleet/data.json" || strings.Join(Def.Needs, ",") != "tool:opa" {
		t.Errorf("%+v", Def)
	}
}

// A tree that is not the die's source cannot be built: the fault is the verdict.
func TestAFaultIsTheVerdict(t *testing.T) {
	t.Setenv(bundlekit.OutEnv, t.TempDir())
	t.Setenv("ATOM_SHA", "0123456789abcdef0123456789abcdef01234567")
	t.Setenv("ATOM_WORK", t.TempDir())
	in, _ := atomtest.ToolTree(t, atomtest.DieTree(nil), atomtest.OpaScript{Version: atomtest.PinnedOpa()}.Answer)
	v := check(context.Background(), in)
	if v.State == int(checks.StatePass) || !strings.Contains(v.Reason, id+":") {
		t.Errorf("%+v", v)
	}
}

func TestAnOpaThatIsNotThePinnedOneNeverRuns(t *testing.T) {
	in, _ := atomtest.ToolTree(t, atomtest.DieTree(nil), atomtest.OpaScript{Version: "Version: 0.1.0"}.Answer)
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "does not answer")
}
