// Package diesauthzgrid is the atom dies:authz-grid: every cell the authz_v2 grids enumerate
// still gets the answer it got when the golden was recorded.
package diesauthzgrid

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:authz-grid", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every cell of the authz_v2 decision, decide and endpoint_decision grids, asked with time.now_ns pinned, still gets the answer the recorded golden holds: one digest per plane, and the first cells that moved when it does not.",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:opa", "tool:uv"},
	Check:     check,
}

const id = "dies:authz-grid"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

const (
	// checker is the tree's own, and its exit is the verdict, unmapped (0 holds,
	// 1 an answer moved, 2 could not run).
	checker = "tools/check_authz_grid.py"
	// golden is what the checker compares against: the digests and the cells.
	golden = "tests/authz_v2/golden/digests.json"
	cells  = "tests/authz_v2/golden/cells.tsv.gz"
	// listed is how many moved cells per plane the verdict names.
	listed = "20"
)

// check runs the tree's checker over the pinned opa. THE GOLDEN IS REQUIRED, not
// optional: a tree with a checker and no golden has nothing to compare with, and
// "no digest to differ from" is not a grid that held. The checker shards the
// evaluation across the cores it may use, so the atom hands it nothing but the
// length of the list it prints.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	if stop := dieskit.Shape(t, row); stop != nil {
		return *stop
	}
	if stop := atoms.RequirePaths(t, row, [][2]string{
		{checker, checker + " is absent, so there is no checker to run."},
		{golden, golden + " is absent, so there is no recorded digest to compare with. Record it with " + checker + " --record."},
		{cells, cells + " is absent, so there is no recorded cell list to name a moved cell from."},
	}); stop != nil {
		return *stop
	}
	if stop := dieskit.OpaProvisioned(ctx, row, in); stop != nil {
		return *stop
	}
	return dieskit.Verdict(ctx, row, in, "", checker, "--first", listed)
}
