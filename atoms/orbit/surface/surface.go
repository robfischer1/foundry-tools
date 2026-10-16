// Package orbitsurface is the atom orbit:surface: a star's code matches its
// contracts, from narcissus's analyzer run over the checkout. One file holds
// the atom and both runners execute it (lld.md FN-085).
package orbitsurface

import (
	"context"
	"errors"
	"fmt"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitcompose"
	"dagger/foundry-tools/internal/orbitkit"
	"dagger/foundry-tools/internal/orbitlane"
)

// Def is the atom. It execs narc, which the tools container mounts per run from
// the pin flux holds live; it reads foundry-dies and the origin.
var Def = atomkit.Def{
	ID: "orbit:surface", Stage: checks.StageOrbit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "A star's code matches its contracts (narcissus's analyzer over the checkout): every contracted verb it serves is registered, every verb it dials is on that pair's contract.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:narc", "dies"},
	Check:     check,
}

const id = "orbit:surface"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageOrbit, Lane: checks.LaneAny}

// check: see the header.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	own, err := orbitkit.FilesIn(t, "orbits/*")
	if err != nil {
		return orbitkit.OrbitCannot(row, err)
	}
	if orbitkit.HasContract(own) {
		return orbitkit.OrbitAbsent(row, "the contracts' repository: each contract is checked against the code in its producer's and consumer's own orbit lane, where the checkout is the code")
	}
	star := orbitkit.StarOf(in.Origin)
	cs, bad, prefixes, err := orbitkit.FleetContractsAndRoster(in)
	if err != nil {
		return orbitkit.OrbitCannot(row, err)
	}
	if !party(star, cs) {
		return orbitkit.OrbitAbsent(row, star+" takes part in no contracted seam in foundry-dies/orbits")
	}
	if in.NarcErr != "" {
		return orbitkit.OrbitCannot(row, errors.New(in.NarcErr))
	}
	// STDOUT ALONE, AND ANY EXIT. narc exits 2 when a site could not be followed
	// and still prints its whole JSON report: the hole is part of the answer. With
	// --json - the report is its only stdout, so stderr is never folded in.
	reports := map[string][]byte{}
	for _, scan := range []string{"surface", "orbits"} {
		out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "narc", Args: []string{"scan", scan, "--json", "-", in.Root}, StdoutOnly: true})
		if code < 0 {
			return orbitkit.OrbitCannot(row, fmt.Errorf("narc scan %s never ran: %s", scan, out))
		}
		reports[scan] = []byte(out)
	}
	found, err := orbitlane.Surface(orbitlane.SurfaceInput{
		Star: star, Contracts: cs, Prefixes: prefixes, Surface: reports["surface"], Orbits: reports["orbits"],
	})
	if err != nil {
		return orbitkit.OrbitCannot(row, err)
	}
	return orbitkit.OrbitVerdict(row, append(found, bad...))
}

// party reports whether star is either side of any contract.
func party(star string, cs []orbitcompose.Contract) bool {
	for _, c := range cs {
		if c.Producer == star || c.Consumer == star {
			return true
		}
	}
	return false
}
