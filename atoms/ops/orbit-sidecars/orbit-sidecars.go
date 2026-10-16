// Package opsorbitsidecars is the atom ops:orbit-sidecars: every composed orbit
// sidecar an ops tree tracks parses with the reader the star itself runs. One
// file holds the atom and both runners execute it (lld.md FN-085).
package opsorbitsidecars

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitkit"
)

// Def is the atom. The reader is orbitparse, a ten-line main over
// policy.ParseACL that the tools container carries.
var Def = atomkit.Def{
	ID: "ops:orbit-sidecars", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every composed orbit sidecar (*.orbit.toml) an ops tree tracks parses with stellar-core-go policy.ParseACL, the reader a witness-mode star refuses to boot without.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:orbitparse"},
	Check:     check,
}

const id = "ops:orbit-sidecars"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// check is the shared question (orbitkit.OrbitSidecars), which orbit:sidecars
// also asks as a finding.
//
// WHY IT EXISTS. A star on mcpserve.Door in witness mode reads
// /etc/stellar/orbit.toml at boot, and stellar-core-go policy.New REFUSES TO
// START on a sidecar that is present and does not parse (an absent one is a
// warning). The sidecars reach that path as ConfigMaps flux renders from
// prime/orbits, so a sidecar the reader rejects would land, roll, and leave its
// star down. This is the same parse, run in the flux pull, before the mount.
//
// THE THREE STATES. 0: every sidecar parses (or the tree tracks none, or is not
// an ops tree). 1: at least one would refuse its star's boot, each named. 2: the
// tool could not be built or a file could not be read - a sidecar nobody parsed
// is not a sidecar that parses.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return orbitkit.OrbitSidecars(ctx, row, in)
}
