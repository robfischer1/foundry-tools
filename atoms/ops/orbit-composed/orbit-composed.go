// Package opsorbitcomposed is the atom ops:orbit-composed: flux's rendered orbit
// sidecars are not stale against foundry-dies main. One file holds the atom and
// both runners execute it (lld.md FN-085).
package opsorbitcomposed

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitkit"
)

// Def is the atom. It reads the tree and foundry-dies, so it runs in lead with
// the dies mounted.
var Def = atomkit.Def{
	ID: "ops:orbit-composed", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The orbit sidecars a tree renders (prime/orbits) are exactly what orbitcompose composes from foundry-dies/orbits at main — nothing a re-render would write or remove.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"dies"},
	Check:     check,
}

const id = "ops:orbit-composed"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// check is the shared question (orbitkit.OrbitComposed), which orbit:sidecars
// also asks as a finding. A tree that renders no sidecars is ABSENT.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	return orbitkit.OrbitComposed(row, in)
}
