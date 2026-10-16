// Package opsworldpatches is the atom ops:world-patches: a World's pod gets the
// pistis.star patches a star Deployment gets. One file holds the atom and both
// runners execute it (lld.md FN-085).
package opsworldpatches

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. The tree's own checker is run with the venv's python.
var Def = atomkit.Def{
	ID: "ops:world-patches", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The repo's own tools/world-patches (flux): every pistis.star patch in prime reaches Worlds as JSON6902, and no World writes out what a patch adds, or it names the drift (a World's pod freezes otherwise).",
	Surface:   "any:**",
	Toolchain: "python",
	Check:     check,
}

const id = "ops:world-patches"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check: a World's pod gets the pistis.star patches a star Deployment
// gets (flux's tools/world-patches, pyyaml). A patch that misses Worlds, or a World
// that writes out what a patch adds, freezes that World's pod (flux#18356). The tool
// grades itself: 1 is drift, 2 is "could not read", could-not-run whatever its words were.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return atomlib.RunChecker(ctx, row, in, atomlib.Checker{Phase: "world-patches", Tool: "world-patches", Python: "python", Two: "\nworld-patches: could not read prime's kustomization or its Worlds — did not look"})
}
