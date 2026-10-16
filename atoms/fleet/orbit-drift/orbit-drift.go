// Package fleetorbitdrift is the atom fleet:orbit-drift: this repo's declared seams agree with
// the canonical contracts. One file holds the atom, and both runners execute it (lld.md FN-085).
package fleetorbitdrift

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It runs in lead: the tree it reads is the pod's, and
// nothing else needs a toolchain.
var Def = atomkit.Def{
	ID: "fleet:orbit-drift", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "This repo's declared seams agree with the canonical contracts in foundry-dies/orbits.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:orbit-drift"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// orbitDrift: this repo's declared seams agree with the canonical contracts. An
// orbit.toml is the surface; without one the repo declares no seams.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	entries, err := t.Entries(".")
	if err != nil {
		return checks.CannotEnumerate(row, err)
	}
	if !checks.HasEntry(entries, "orbit.toml") {
		return checks.VerdictOf(row, int(checks.StatePass), "fleet:orbit-drift: ABSENT - no orbit.toml in this tree, so this repo declares no seams")
	}
	declared, err := t.Read("orbit.toml")
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), "fleet:orbit-drift: CANNOT RUN - orbit.toml did not parse: "+err.Error())
	}
	state, report := checks.OrbitDrift(ctx, declared, in.DoorOf())
	return checks.VerdictOf(row, state, report)
}
