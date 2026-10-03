package orbitlane

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitcompose"
)

// orbit:surface — a star's code still matches its contracts, in the star's
// own pull. Two directions, each from narcissus's own analyzer run over the
// checkout (`narc scan surface` and `narc scan orbits`, the reads
// code_query answers on the wire), never a parser of this module's:
//
//   - PRODUCER: every verb a contract says a consumer may call on this star is
//     one this star REGISTERS. A pull that renames or removes a served verb
//     without touching the contract is the finding.
//   - CONSUMER: every verb this star's source DIALS at another star is on that
//     pair's contract. A pull that adds a call without touching the orbit is
//     the finding.
//
// ATTRIBUTION, AND ITS LIMIT. narcissus reads nearly every dial as a "loose
// verb": the verb is certain, the producer is not named at the call site. A
// loose verb is attributed through the contracts themselves: it holds when a
// contract with this star as consumer names it; it is this star's own when
// this star registers it; otherwise it belongs to the one producer whose
// contracts name it — and is a finding against the pair's contract. A verb no
// contract names, or several producers' do, cannot be attributed: that is
// `unanalyzable`, never `holds`. A gateway-prefixed name (graph_quads_to) is
// matched to a producer only through THAT producer's roster verb_prefix.

// SurfaceInput is what the atom read: the star, every contract in the fleet,
// the roster's verb prefix per star, and narcissus's two JSON reports over the
// star's checkout.
type SurfaceInput struct {
	Star      string
	Contracts []orbitcompose.Contract
	Prefixes  map[string]string
	Surface   []byte
	Orbits    []byte
}

type narcReport struct {
	Findings []checks.Finding `json:"findings"`
}

// dial is one verb the source dials, and where.
type dial struct{ verb, where string }

// Surface judges both directions. A report that is not narcissus's JSON is an
// error: the analyzer's answer could not be read, which is not a pass.
func Surface(in SurfaceInput) ([]checks.Finding, error) {
	registered, unresolved, err := readSurface(in.Surface)
	if err != nil {
		return nil, fmt.Errorf("narc scan surface answered something that is not its report: %v", err)
	}
	dials, err := readDials(in.Orbits)
	if err != nil {
		return nil, fmt.Errorf("narc scan orbits answered something that is not its report: %v", err)
	}
	out := produced(in, registered, unresolved)
	out = append(out, dialled(in, registered, dials)...)
	if len(out) == 0 {
		out = append(out, finding(checks.VerdictInert, in.Star, "no-seams", in.Star+" serves no contracted verb and dials none"))
	}
	return out, nil
}

// readSurface answers the verbs a star registers and how many registration
// sites narcissus could not follow.
func readSurface(raw []byte) (map[string]bool, int, error) {
	var r narcReport
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, 0, err
	}
	registered, unresolved := map[string]bool{}, 0
	for _, f := range r.Findings {
		if f.Verdict == checks.VerdictUnanalyzable {
			unresolved++
			continue
		}
		registered[f.Subject] = true
	}
	return registered, unresolved, nil
}

// readDials answers each loose verb the source dials, once, at its first site.
func readDials(raw []byte) ([]dial, error) {
	var r narcReport
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []dial
	for _, f := range r.Findings {
		verb, _, _ := strings.Cut(f.Detail, " — ")
		if f.Cause != "loose-verb" || seen[verb] {
			continue
		}
		seen[verb] = true
		out = append(out, dial{verb, f.Subject})
	}
	return out, nil
}

// produced is the producer direction: one finding per contracted verb the star
// does not register, and one holds per contract that agrees.
func produced(in SurfaceInput, registered map[string]bool, unresolved int) []checks.Finding {
	var out []checks.Finding
	for _, c := range in.Contracts {
		if c.Producer != in.Star {
			continue
		}
		missing := slices.DeleteFunc(slices.Clone(c.Verbs), func(v string) bool { return registered[v] })
		if len(missing) == 0 {
			out = append(out, finding(checks.VerdictHolds, "orbits/"+c.Name+".toml", "served",
				fmt.Sprintf("%s registers all %d verb(s) %s may call", in.Star, len(c.Verbs), c.Consumer)))
			continue
		}
		for _, v := range missing {
			if unresolved > 0 {
				out = append(out, finding(checks.VerdictUnanalyzable, in.Star+" "+v, "registration-unresolved",
					fmt.Sprintf("orbits/%s.toml names %s, it is not among the verbs %s registers, and narcissus could not follow %d registration site(s), so it may be one of them", c.Name, v, in.Star, unresolved)))
				continue
			}
			out = append(out, finding(checks.VerdictViolated, in.Star+" "+v, "contracted-verb-unregistered",
				fmt.Sprintf("orbits/%s.toml says %s may call %s, and %s registers no %s — if this pull renamed or removed it, edit orbits/%s.toml in a foundry-dies pull to match",
					c.Name, c.Consumer, v, in.Star, v, c.Name)))
		}
	}
	return out
}

// dialled is the consumer direction.
func dialled(in SurfaceInput, registered map[string]bool, dials []dial) []checks.Finding {
	var out []checks.Finding
	held, own := 0, 0
	for _, d := range dials {
		if registered[d.verb] {
			own++
			continue
		}
		if len(in.serving(d.verb, true)) > 0 {
			held++
			continue
		}
		producers := in.serving(d.verb, false)
		if len(producers) != 1 {
			out = append(out, finding(checks.VerdictUnanalyzable, d.where, "dial-unattributed",
				fmt.Sprintf("%s dials %s and no single producer's contracts name it (%d do) — if it is a new seam, its contract is orbits/<producer>-%s.toml, where <producer> is the star that registers %s",
					in.Star, d.verb, len(producers), in.Star, d.verb)))
			continue
		}
		p, native := producers[0].producer, producers[0].native
		out = append(out, finding(checks.VerdictViolated, d.where, "dial-uncontracted",
			fmt.Sprintf("%s dials %s, which %s serves, and orbits/%s-%s.toml does not name it — add %q to its verbs in a foundry-dies pull (create the file if the pair has none)",
				in.Star, d.verb, p, p, in.Star, native)))
	}
	if held+own > 0 {
		out = append(out, finding(checks.VerdictHolds, in.Star, "dials-contracted",
			fmt.Sprintf("%d dialled verb(s) are on %s's contracts, %d are its own", held, in.Star, own)))
	}
	return out
}

// served is one producer a verb is contracted on, and the verb's native name
// there.
type served struct{ producer, native string }

// serving answers the distinct producers whose contracts name verb — only
// those with this star as consumer when mine, any consumer otherwise. The
// name matches whole, or with the producer's own roster verb_prefix stripped.
func (in SurfaceInput) serving(verb string, mine bool) []served {
	seen := map[string]bool{}
	var out []served
	for _, c := range in.Contracts {
		if mine && c.Consumer != in.Star {
			continue
		}
		// The whole name first; then the name with THIS producer's prefix cut,
		// which is the name unchanged when it carries no such prefix (and no
		// verb in the fleet begins with the bare "_" an empty prefix cuts).
		native := verb
		if !slices.Contains(c.Verbs, verb) {
			native = strings.TrimPrefix(verb, in.Prefixes[c.Producer]+"_")
			if !slices.Contains(c.Verbs, native) {
				continue
			}
		}
		if !seen[c.Producer] {
			seen[c.Producer] = true
			out = append(out, served{c.Producer, native})
		}
	}
	return out
}
