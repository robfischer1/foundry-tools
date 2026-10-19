// Package bundleresign is the atom bundle:resign: the KMS signature written into the OPA twins and verified. It is the bundle lane's alone
// (stage bundle is in no other lane's stages); the work is internal/bundlekit's.
package bundleresign

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/bundlekit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

const id = "bundle:resign"

// Def is the atom.
var Def = atomkit.Def{
	ID: "bundle:resign", Stage: checks.StageBundle, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The OPA twins carry the KMS signature and load under the key id ouranos pins.",
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
	return bundlekit.Verdict(row, bundlekit.Resign(ctx, bundlekit.Run{In: in, Row: row}), "the KMS signature built and gated")
}
