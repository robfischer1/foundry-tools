package orbitcompose

import (
	"fmt"
	"strings"
)

// THE REFERENCE DIALECT — the orbit.toml a star's REPOSITORY carries (Rob,
// 2026-10-03: "the orbits.toml containing a ref to the contract details").
// Each edge names its counterparty and the contract by name, version and
// content digest; the verbs stay in the contract, reached by the reference.
// It is the file a session reads to learn its blast radius without cloning
// anything, and the dialect nyx's hand-written repo-root orbit.toml already
// speaks (fleet:orbit-drift compares its digests).
//
// THE INLINED FORM (Render) IS A RUNTIME RENDERING, not this. It is what the
// pod mounts at /etc/stellar/orbit.toml for policy.ParseACL, which needs the
// verbs in hand. Both are composed from the same contracts here, so they
// cannot disagree about which seams exist.

// RenderReference is a star's reference-dialect orbit.toml: [[produces]]
// to/contract/version/digest, then [[consumes]] from/... the same. No clock,
// no map order: the same contracts render the same bytes.
func RenderReference(s Sidecar) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# orbit.toml — %s's seams, by reference. Laid from the data/orbits die\n", s.Star)
	b.WriteString("# (foundry-tools orbitcompose over foundry-dies/orbits). Generated — do not\n")
	b.WriteString("# hand-edit: a local edit is drift, and the next lay overwrites it.\n")
	b.WriteString("#\n")
	b.WriteString("# Each edge names a contract, foundry-dies/orbits/<contract>.toml, by name,\n")
	b.WriteString("# version and sha256 of its bytes. The verbs live in the contract.\n")
	fmt.Fprintf(&b, "# [[produces]] is who breaks if %s changes what it SERVES; [[consumes]] is\n", s.Star)
	fmt.Fprintf(&b, "# what %s breaks if it changes how it CALLS. To change a seam, edit the\n", s.Star)
	b.WriteString("# contract in a foundry-dies pull (orbits/README.md says how), then re-lay\n")
	b.WriteString("# from the repo root: furnace die --for . --dest DIR && gavel order . --from DIR\n")
	for _, c := range s.Produces {
		reference(&b, "produces", "to", c.Consumer, c)
	}
	for _, c := range s.Consumes {
		reference(&b, "consumes", "from", c.Producer, c)
	}
	return []byte(b.String())
}

// reference writes one [[table]] entry naming the peer and pinning the
// contract.
func reference(b *strings.Builder, table, key, peer string, c Contract) {
	fmt.Fprintf(b, "\n[[%s]]\n", table)
	fmt.Fprintf(b, "%s = %q\n", key, peer)
	fmt.Fprintf(b, "contract = %q\n", c.Name)
	fmt.Fprintf(b, "version = %q\n", c.Version)
	fmt.Fprintf(b, "digest = %q\n", c.Digest)
}
