// Package bundleorbits is the atom bundle:orbits: the orbit dies (data/contracts, data/orbits) composed from foundry-dies/orbits. It is the bundle lane's alone
// (stage bundle is in no other lane's stages); the work is internal/bundlekit's.
package bundleorbits

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/bundlekit"
	"dagger/foundry-tools/internal/checks"
)

const id = "bundle:orbits"

// Def is the atom.
var Def = atomkit.Def{
	ID: "bundle:orbits", Stage: checks.StageBundle, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The seam contracts compose, and data/contracts and data/orbits are built from them.",
	Surface:   "all:policy/.manifest,fleet/data.json",
	Toolchain: "none",
	Needs:     nil,
	Timeout:   20 * time.Minute,
	Check:     check,
}

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageBundle, Lane: checks.LaneAny}

func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return bundlekit.Verdict(row, bundlekit.Orbits(ctx, bundlekit.Run{In: in, Row: row}), "the orbit dies built and gated")
}
