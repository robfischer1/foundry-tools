package atoms

import (
	"context"
	"errors"
	"fmt"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitlane"
)

// ORBIT:SURFACE IN THE BINARY: the star's code against its contracts, from
// narcissus's analyzer run over the checkout. The chain is atoms_orbit_lane.go's
// orbitSurface; the judgement is orbitlane.Surface, the states are the chain's.
//
// WHAT IT CALLS, AND WHERE THE NETWORK IS. It execs `narc`, narcissus's own
// analyzer (`narc scan` is the table code_query answers on the wire; it takes no
// store and no door). The network is not in the exec but around it: the module
// reads the image tag and digest flux pins LIVE from foundry/flux main and pulls
// that image to get the binary, and mounts it on PATH for the run (the module's
// withAtomNarc). A pin that could not be read, or an image that would not pull,
// arrives as Input.NarcErr and settles 2 with that reason, as the chain's
// narcissusRef error did; there is no fallback to :stable.
//
// REPORT-ONLY until orbitlane.Enforce flips, exactly as the chain is.

// orbitSurface: see the header.
func orbitSurface(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	own, err := filesIn(t, "orbits/*")
	if err != nil {
		return orbitCannot(a, err)
	}
	if hasContract(own) {
		return orbitAbsent(a, "the contracts' repository: each contract is checked against the code in its producer's and consumer's own orbit lane, where the checkout is the code")
	}
	star := starOf(in.Origin)
	cs, bad, err := fleetContracts(in)
	if err != nil {
		return orbitCannot(a, err)
	}
	if !party(star, cs) {
		return orbitAbsent(a, star+" takes part in no contracted seam in foundry-dies/orbits")
	}
	dies, err := in.dies()
	if err != nil {
		return orbitCannot(a, err)
	}
	prefixes, err := roster(dies)
	if err != nil {
		return orbitCannot(a, fmt.Errorf("foundry-dies: %v", err))
	}
	if in.NarcErr != "" {
		return orbitCannot(a, errors.New(in.NarcErr))
	}
	// STDOUT ALONE, AND ANY EXIT. narc exits 2 when a site could not be followed
	// and still prints its whole JSON report: the hole is part of the answer. With
	// --json - the report is its only stdout, so stderr is never folded in.
	reports := map[string][]byte{}
	for _, scan := range []string{"surface", "orbits"} {
		out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "narc", Args: []string{"scan", scan, "--json", "-", in.Root}, StdoutOnly: true})
		if code < 0 {
			return orbitCannot(a, fmt.Errorf("narc scan %s never ran: %s", scan, out))
		}
		reports[scan] = []byte(out)
	}
	found, err := orbitlane.Surface(orbitlane.SurfaceInput{
		Star: star, Contracts: cs, Prefixes: prefixes, Surface: reports["surface"], Orbits: reports["orbits"],
	})
	if err != nil {
		return orbitCannot(a, err)
	}
	return orbitVerdict(a, append(found, bad...))
}
