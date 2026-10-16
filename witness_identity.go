package main

import "dagger/foundry-tools/internal/dagger"

// THE WITNESS ASKS AS THE LANE'S RUN, WHEN THE LANE GAVE IT ONE.
//
// fleet:witness asked from this module's own process, inside the dagger ENGINE,
// so narcissus saw the engine's pod IP and no certificate: measured 2026-10-04
// (tcpdump on dev01), the four witness SYNs of a gate came from the engine and
// none from the lane pod, and narcissus recorded every gate's ask as
// `unidentified`. Under enforce it would refuse every gate.
//
// With --spire the lane pod forwards its SPIRE agent socket. The agent attests
// the lane pod, and its entry (flux ci-jobs ClusterSPIFFEID) names the run:
// spiffe://notusmi.com/job/gate/<job>. The atom (atoms/fleet/witness) runs in a
// container with that socket mounted (atom_env.go) and asks narcissus's mTLS
// door with the SVID witnesscall fetches into its own memory; the key is never a
// file, a secret or a layer.

// withSpire hands the run the lane pod's forwarded SPIRE socket (nil: none).
func (r *run) withSpire(s *dagger.Socket) *run {
	r.spire = s
	return r
}
