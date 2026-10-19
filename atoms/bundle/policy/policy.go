// Package bundlepolicy is the atom bundle:policy: the policy die built plain and as a twin the KMS signs, from policy/ with its decision table. It is the bundle lane's alone
// (stage bundle is in no other lane's stages); the work is internal/bundlekit's.
package bundlepolicy

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/bundlekit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

const id = "bundle:policy"

// Def is the atom.
var Def = atomkit.Def{
	ID: "bundle:policy", Stage: checks.StageBundle, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The policy die builds plain and as a KMS-signed twin from policy/ with its decision table, and the artifact carries its data and hides what it should.",
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
	return bundlekit.Verdict(row, bundlekit.Policy(ctx, bundlekit.Run{In: in, Row: row}), "the policy die built and gated")
}
