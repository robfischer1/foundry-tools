package orbitlane

import (
	"fmt"

	"github.com/BurntSushi/toml"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitcompose"
)

// orbit:repo — a star's laid orbit.toml is what the contracts compose to for
// that star. Runs on a star's repository when it carries a root orbit.toml.
//
// IT IS fleet:orbit-drift's DIGEST COMPARISON, FOLDED IN (Rob, 2026-10-03):
// each edge's digest is compared with sha256 of the canonical contract, and
// beyond that the EDGE SET is compared with the composition — an edge the
// contracts carry and the file does not, or the reverse, is the same drift a
// moved digest is. It reads the reference keys (to/from, contract, version,
// digest); a file that also inlines verbs or status (nyx's, hand-written
// before the die) is judged on the keys it shares with the reference dialect,
// and the extra keys are not graded.

// RelayHint is how a star brings its orbit.toml back to the die: the
// governance rail lays it (furnace renders it from the signed data/orbits die,
// gavel lays it and records it), and the result is committed through the
// repo's normal landing.
const RelayHint = "re-lay it from the die, in a worktree at the repo root: furnace die --for . --dest DIR && gavel order . --from DIR, then commit orbit.toml"

type laidEdge struct {
	To       string `toml:"to"`
	From     string `toml:"from"`
	Contract string `toml:"contract"`
	Version  string `toml:"version"`
	Digest   string `toml:"digest"`
}

type laidDoc struct {
	Produces []laidEdge `toml:"produces"`
	Consumes []laidEdge `toml:"consumes"`
}

// Repo judges star's laid orbit.toml against the contracts.
func Repo(star string, laid []byte, contracts []orbitcompose.Contract) []checks.Finding {
	var doc laidDoc
	if _, err := toml.Decode(string(laid), &doc); err != nil {
		return []checks.Finding{finding(checks.VerdictViolated, "orbit.toml", "orbit-toml-unparseable", err.Error()+"; "+RelayHint)}
	}
	want := map[string]orbitcompose.Contract{}
	for _, c := range contracts {
		if c.Producer == star {
			want["produces "+c.Consumer] = c
		}
		if c.Consumer == star {
			want["consumes "+c.Producer] = c
		}
	}
	have := map[string]laidEdge{}
	for _, e := range doc.Produces {
		have["produces "+e.To] = e
	}
	for _, e := range doc.Consumes {
		have["consumes "+e.From] = e
	}
	keys := map[string]bool{}
	for k := range want {
		keys[k] = true
	}
	for k := range have {
		keys[k] = true
	}
	var out []checks.Finding
	for _, k := range sortedKeys(keys) {
		out = append(out, edge(k, want, have))
	}
	if len(out) == 0 {
		out = append(out, finding(checks.VerdictInert, "orbit.toml", "no-seams", star+" takes part in no contracted seam, and the file names none"))
	}
	return out
}

// edge judges one (direction, peer) against the composition.
func edge(key string, want map[string]orbitcompose.Contract, have map[string]laidEdge) checks.Finding {
	c, contracted := want[key]
	e, laid := have[key]
	switch {
	case !laid:
		return finding(checks.VerdictDrifted, key, "edge-missing",
			fmt.Sprintf("contract %s exists and orbit.toml does not name it; %s", c.Name, RelayHint))
	case !contracted:
		return finding(checks.VerdictDrifted, key, "edge-uncontracted",
			fmt.Sprintf("orbit.toml names %q and no contract in foundry-dies/orbits backs this edge; %s", e.Contract, RelayHint))
	case e.Contract != c.Name || e.Version != c.Version || e.Digest != c.Digest:
		return finding(checks.VerdictDrifted, key, "edge-stale",
			fmt.Sprintf("orbit.toml pins %s v%s %s; the contract is %s v%s %s — the contract moved and this file did not; %s",
				e.Contract, e.Version, e.Digest, c.Name, c.Version, c.Digest, RelayHint))
	}
	return finding(checks.VerdictHolds, key, "edge", c.Name+" v"+c.Version+" "+c.Digest)
}
