// Package opsdup is the atom ops:dup: the repo's own tools/dup-check finds no
// blocking duplicate. One file holds the atom and both runners execute it
// (lld.md FN-085).
package opsdup

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. The tree's own checker is run with the venv's python.
var Def = atomkit.Def{
	ID: "ops:dup", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The repo's own tools/dup-check (infra) finds no blocking duplicate; ABSENT where the tree has no such tool.",
	Surface:   "any:**",
	Toolchain: "python",
	Check:     check,
}

const id = "ops:dup"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check: the repo's own tools/dup-check finds no blocking duplicate.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return atomlib.RunChecker(ctx, row, in, atomlib.Checker{Phase: "dup", Tool: "dup-check", Executable: true, Python: "python3", Args: []string{"--blocking"}})
}
