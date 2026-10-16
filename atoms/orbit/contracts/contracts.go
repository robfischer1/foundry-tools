// Package orbitcontracts is the atom orbit:contracts: every seam contract in
// foundry-dies/orbits parses, names two stars on the roster and is approved.
// One file holds the atom and both runners execute it (lld.md FN-085).
package orbitcontracts

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitkit"
	"dagger/foundry-tools/internal/orbitlane"
)

// Def is the atom. It reads the contracts' own tree, so it runs in lead.
var Def = atomkit.Def{
	ID: "orbit:contracts", Stage: checks.StageOrbit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every seam contract in foundry-dies/orbits parses, names two stars on the roster, and carries the one status a rendered fact has, approved.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "orbit:contracts"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageOrbit, Lane: checks.LaneAny}

// check: every contract parses, names two stars on the roster, carries
// and carries the one status a rendered fact has, approved.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	const notHere = "this tree is not the contracts' repository (it needs orbits/*.toml beside fleet/repos/)"
	t := in.Tree()
	files, err := orbitkit.FilesIn(t, "orbits/*")
	if err != nil {
		return orbitkit.OrbitCannot(row, err)
	}
	if !orbitkit.HasContract(files) {
		return orbitkit.OrbitAbsent(row, notHere)
	}
	stars, err := orbitkit.Roster(t)
	if err != nil {
		return orbitkit.OrbitCannot(row, err)
	}
	if len(stars) == 0 {
		return orbitkit.OrbitAbsent(row, notHere)
	}
	return orbitkit.OrbitVerdict(row, orbitlane.Contracts(files, orbitkit.StarSet(stars)))
}
