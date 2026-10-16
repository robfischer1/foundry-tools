package lawverbliveness

import (
	"context"
	"os"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

// TestUT_485_TheDeclarationIsTheRowItReplaced: the Def carries the identity the
// catalogue row did, and validates.
func TestUT_485_TheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "none" || Def.Surface != "any:**" || len(Def.Needs) != 0 {
		t.Errorf("%+v", Def)
	}
}

func TestIsAbsentOffFoundryStocks(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{
		"governance/blocks/Bad_Name.md": "",
		"renders/c/claude/AGENTS.md":    "`athena_quick_add`",
	})
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Absent, "not foundry-stocks")
}

func TestPassesCurrentLaw(t *testing.T) {
	spans := `[{"end_byte":32,"position":0,"slug":"forge","start_byte":0}]`
	in := atomtest.TreeIn(t, atomtest.StocksTree(map[string]string{
		"renders/forge-root/claude/AGENTS.md":           "CI logs through `repo_ci_logs`.\n",
		"renders/forge-root/claude/.furnace/spans.json": spans,
		"budget.toml": "[budget.forge-root]\nclaude = 40\n",
	}))
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
}

func TestIsRedOnTheAugustText(t *testing.T) {
	in := atomtest.TreeIn(t, atomtest.StocksTree(map[string]string{
		"governance/blocks/forge.md": "CI logs through `repo_ci_logs`.\nPin a task with `athena_quick_add`.\n",
	}))
	atomtest.Expect(t, check(context.Background(), in), checks.StateFindings, atomtest.Findings,
		"governance/blocks/forge.md:2  athena_quick_add is not a verb on hades's surface")
}

func TestCannotRunOnATreeThatWillNotWalk(t *testing.T) {
	atomtest.Expect(t, check(context.Background(), atomtest.MissingRoot(t)), checks.StateCannotRun, atomtest.Cannot, "the tree would not enumerate")
	in := atomtest.TreeIn(t, atomtest.StocksTree(nil))
	in.FS = atomtest.FailingFS{FS: os.DirFS(in.Root), Dir: "governance"}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the tree would not enumerate: boom")
}
