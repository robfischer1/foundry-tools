package orbitcompose

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Sidecar is one star's composed seams: the edges it serves and the edges it
// dials.
type Sidecar struct {
	Star     string
	Produces []Contract
	Consumes []Contract
}

// Compose folds the contracts into one sidecar per star that takes part in
// any, sorted by star.
//
// ONE CONTRACT PER PAIR IS THE DIRECTORY'S, NOT THIS FUNCTION'S. The reader
// refuses a sidecar naming one consumer in two [[produces]] blocks, and a pair
// can only be contracted twice by two files with one name — which a directory
// cannot hold. A caller that builds its own slice and repeats a pair gets the
// two blocks, and the flux gate's parse (ops:orbit-sidecars, the reader
// itself) refuses them before any star could.
func Compose(contracts []Contract) []Sidecar {
	byStar := map[string]*Sidecar{}
	get := func(star string) *Sidecar {
		if byStar[star] == nil {
			byStar[star] = &Sidecar{Star: star}
		}
		return byStar[star]
	}
	for _, c := range contracts {
		get(c.Producer).Produces = append(get(c.Producer).Produces, c)
		// A CI lane is no star: it has no sidecar, no ConfigMap and no PEP.
		// Its edge lives on the producer's side alone.
		if !IsJob(c.Consumer) {
			get(c.Consumer).Consumes = append(get(c.Consumer).Consumes, c)
		}
	}
	out := make([]Sidecar, 0, len(byStar))
	for _, s := range byStar {
		slices.SortFunc(s.Produces, func(a, b Contract) int { return cmp.Compare(a.Consumer, b.Consumer) })
		slices.SortFunc(s.Consumes, func(a, b Contract) int { return cmp.Compare(a.Producer, b.Producer) })
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b Sidecar) int { return cmp.Compare(a.Star, b.Star) })
	return out
}

// Render is the sidecar's bytes in the composed-sidecar dialect
// (stellar-core-go policy/acl.go): [[produces]] to/contract/version/status/
// wire_form/verbs, then [[consumes]] from/... the same. No clock, no map
// order: the same contracts render the same bytes.
func Render(s Sidecar) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# orbit.toml — %s's seam contracts, composed by foundry-tools orbitcompose\n", s.Star)
	b.WriteString("# from foundry-dies/orbits. [[produces]] is the inbound ACL the endpoint PEP\n")
	b.WriteString("# reads; [[consumes]] references the producer's contract. Generated — do not\n")
	b.WriteString("# hand-edit: change the contract and re-run the composer.\n")
	for _, c := range s.Produces {
		block(&b, "produces", "to", c.Consumer, c)
	}
	for _, c := range s.Consumes {
		block(&b, "consumes", "from", c.Producer, c)
	}
	return []byte(b.String())
}

// block writes one [[table]] entry naming the peer under key.
func block(b *strings.Builder, table, key, peer string, c Contract) {
	fmt.Fprintf(b, "\n[[%s]]\n", table)
	fmt.Fprintf(b, "%s = %q\n", key, peer)
	fmt.Fprintf(b, "contract = %q\n", c.Name)
	fmt.Fprintf(b, "version = %q\n", c.Version)
	fmt.Fprintf(b, "status = %q\n", c.Status)
	fmt.Fprintf(b, "wire_form = %q\n", c.WireForm)
	quoted := make([]string, len(c.Verbs))
	for i, v := range c.Verbs {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	fmt.Fprintf(b, "verbs = [%s]\n", strings.Join(quoted, ", "))
}
