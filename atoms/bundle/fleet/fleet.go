// Package bundlefleet is the atom bundle:fleet: the roster die built from the catalog entries and flux's facts. It is the bundle lane's alone
// (stage bundle is in no other lane's stages); the work is internal/bundlekit's.
package bundlefleet

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/bundlekit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

const id = "bundle:fleet"

// Def is the atom.
var Def = atomkit.Def{
	ID: "bundle:fleet", Stage: checks.StageBundle, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The fleet roster die builds from the staged catalog entries and flux's facts, carries the roster it claims to, and composes with the policy.",
	Surface:   "all:policy/.manifest,fleet/data.json",
	Toolchain: "none",
	Needs:     []string{"tool:opa"},
	Timeout:   20 * time.Minute,
	Check:     check,
}

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageBundle, Lane: checks.LaneAny}

func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if stop := dieskit.OpaProvisioned(ctx, row, in); stop != nil {
		return *stop
	}
	return bundlekit.Verdict(row, bundlekit.Fleet(ctx, bundlekit.Run{In: in, Row: row}), "the roster die built and gated")
}
