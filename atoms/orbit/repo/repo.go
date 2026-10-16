// Package orbitrepo is the atom orbit:repo: a star's laid orbit.toml is what
// the contracts compose to for it. One file holds the atom and both runners
// execute it (lld.md FN-085).
package orbitrepo

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitkit"
	"dagger/foundry-tools/internal/orbitlane"
)

// Def is the atom. It reads the star's tree and foundry-dies, so it runs in
// lead with the dies mounted and the origin handed over.
var Def = atomkit.Def{
	ID: "orbit:repo", Stage: checks.StageOrbit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "A star's laid orbit.toml names every seam the contracts give it, each pinned to the contract's current version and digest.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"dies"},
	Check:     check,
}

const id = "orbit:repo"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageOrbit, Lane: checks.LaneAny}

// check: a laid orbit.toml is what the contracts compose to for this star
// (fleet:orbit-drift's digest comparison, plus the edge set).
func check(_ context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	entries, err := t.Entries(".")
	if err != nil {
		return orbitkit.OrbitCannot(row, err)
	}
	if !checks.HasEntry(entries, "orbit.toml") {
		return orbitkit.OrbitAbsent(row, "this repository carries no root orbit.toml (it is laid from the data/orbits die)")
	}
	laid, err := t.Read("orbit.toml")
	if err != nil {
		return orbitkit.OrbitCannot(row, err)
	}
	cs, bad, err := orbitkit.FleetContracts(in)
	if err != nil {
		return orbitkit.OrbitCannot(row, err)
	}
	return orbitkit.OrbitVerdict(row, append(orbitlane.Repo(orbitkit.StarOf(in.Origin), []byte(laid), cs), bad...))
}
