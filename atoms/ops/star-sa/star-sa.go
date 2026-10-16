// Package opsstarsa is the atom ops:star-sa: the star-SA admission policy's
// generated SA list is the ClusterSPIFFEID CRs' SA list. One file holds the
// atom and both runners execute it (lld.md FN-085).
package opsstarsa

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. The tree's own checker is run with the venv's python.
var Def = atomkit.Def{
	ID: "ops:star-sa", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The repo's own tools/star-sa-list (flux): the star-SA admission policy's generated SA list is the ClusterSPIFFEID CRs' SA list, or it names the drift (a stale list makes star-sa-guard fail open).",
	Surface:   "any:**",
	Toolchain: "python",
	Check:     check,
}

const id = "ops:star-sa"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check: the star-SA admission policy's generated SA list is the
// ClusterSPIFFEID CRs' SA list (flux's tools/star-sa-list, stdlib only). A stale
// list makes the star-sa-guard VAP fail open. The tool grades itself: 1 is stale,
// 2 is "could not read", could-not-run whatever its words were.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return atomlib.RunChecker(ctx, row, in, atomlib.Checker{Phase: "star-sa", Tool: "star-sa-list", Python: "python", Two: "\nstar-sa: could not read the ClusterSPIFFEID CRs or the policy — did not look"})
}
