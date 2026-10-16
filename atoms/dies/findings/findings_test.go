package diesfindings

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// TestUT_485_TheDeclarationIsTheRowItReplaced: the Def carries the identity the
// catalogue row did, and validates.
func TestUT_485_TheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "python" || Def.Surface != "any:**" || strings.Join(Def.Needs, ",") != "tool:uv" {
		t.Errorf("%+v", Def)
	}
}

func TestRunsTheTreesCheckerUnderUVWithJSONSchema(t *testing.T) {
	atomtest.CheckerSuite(t, check, "tools/check_findings.py", dieskit.WithJSONSchema, map[string]string{"schema/findings.schema.json": "{}"})
}

func TestNeedsItsSchemaToo(t *testing.T) {
	files := atomtest.DieTree(map[string]string{"tools/check_findings.py": "x"})
	in, f := atomtest.UVTree(t, files, nil)
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "schema/findings.schema.json is absent, so there is no schema to validate")
	if len(f.Calls) != 0 {
		t.Errorf("uv ran: %v", f.Ran())
	}
}
