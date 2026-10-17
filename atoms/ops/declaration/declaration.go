// Package opsdeclaration is the atom ops:declaration: the repo's own
// tools/declaration-integrity holds. One file holds the atom and both runners
// execute it (lld.md FN-085).
package opsdeclaration

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. The tree's own checker is run with the venv's python.
var Def = atomkit.Def{
	ID: "ops:declaration", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The repo's own tools/declaration-integrity (infra) holds: every declared host file has its payload and every payload is declared.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"pytools"},
	Check:     check,
}

const id = "ops:declaration"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check: the repo's own tools/declaration-integrity holds. Run through
// `python` and not `python3`, as the chain's argv named it; the venv has both.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return atomlib.RunChecker(ctx, row, in, atomlib.Checker{Phase: "declaration", Tool: "declaration-integrity", Python: "python"})
}
