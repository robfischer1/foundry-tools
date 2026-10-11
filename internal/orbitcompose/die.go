package orbitcompose

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
)

// THE DIE PAYLOAD (data/orbits). The flux directory delivers each star its own
// sidecar; the die is the WHOLE GRAPH in one readable artifact — who may call
// whom, over what, with which verbs — so a person or a tool answers a
// producer/consumer question without parsing anybody's code. It is the
// original orbit premise (2026-07-03): the seam is visible from a file, not
// derived from the source.
//
// SHAPE. Every star's REFERENCE-dialect orbit.toml as <star>.orbit.toml
// (RenderReference — the file laid into the star's repository root; the
// inlined runtime form stays flux's), plus orbits.json. orbits.json keeps the frozen
// 2026-08-01 shape — {generated_from, edges[{producer, consumer, contract,
// version, status, verbs}]} — and adds, per edge, `via` (the
// transport; every contract today is an MCP verb seam) and `digest` (sha256 of
// the contract file's bytes, the comparison fleet:orbit-drift makes), and a
// top-level `stars` index: per star, the consumers it produces for and the
// producers it consumes from.
//
// DETERMINISTIC. No clock and no source revision in the bytes: the same
// contracts compose the same payload, so a republish of an unchanged graph is
// the same digest. The revision the payload was built from rides the
// artifact's annotation, where it describes the publish rather than the graph.

// DieIndex is the die's edge list file.
const DieIndex = "orbits.json"

// DieGeneratedFrom is orbits.json's provenance line.
const DieGeneratedFrom = "foundry-dies/orbits (composed by foundry-tools orbitcompose)"

// ViaMCP is the transport of a contract that names none: a verb seam.
const ViaMCP = "mcp"

// dieEdge is one ordered pair, in the frozen file's key order.
type dieEdge struct {
	Producer string   `json:"producer"`
	Consumer string   `json:"consumer"`
	Contract string   `json:"contract"`
	Via      string   `json:"via"`
	Version  string   `json:"version"`
	Status   string   `json:"status"`
	Verbs    []string `json:"verbs"`
	Digest   string   `json:"digest"`
}

// dieStar is one star's place in the graph, by peer name.
type dieStar struct {
	Produces []string `json:"produces"`
	Consumes []string `json:"consumes"`
}

// dieDoc is orbits.json.
type dieDoc struct {
	GeneratedFrom string             `json:"generated_from"`
	Edges         []dieEdge          `json:"edges"`
	Stars         map[string]dieStar `json:"stars"`
}

// Digest is the content digest of a contract file's bytes.
func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Index renders orbits.json for the contracts: one edge per contract, sorted
// by producer then consumer, and the stars index Compose's sidecars imply.
func Index(contracts []Contract) []byte {
	doc := dieDoc{GeneratedFrom: DieGeneratedFrom, Edges: []dieEdge{}, Stars: map[string]dieStar{}}
	sorted := slices.Clone(contracts)
	slices.SortFunc(sorted, func(a, b Contract) int {
		return cmp.Or(cmp.Compare(a.Producer, b.Producer), cmp.Compare(a.Consumer, b.Consumer))
	})
	for _, c := range sorted {
		doc.Edges = append(doc.Edges, dieEdge{
			Producer: c.Producer, Consumer: c.Consumer, Contract: c.Name, Via: c.Via,
			Version: c.Version, Status: c.Status, Verbs: c.Verbs, Digest: c.Digest,
		})
	}
	for _, s := range Compose(contracts) {
		star := dieStar{Produces: []string{}, Consumes: []string{}}
		for _, c := range s.Produces {
			star.Produces = append(star.Produces, c.Consumer)
		}
		for _, c := range s.Consumes {
			star.Consumes = append(star.Consumes, c.Producer)
		}
		doc.Stars[s.Star] = star
	}
	// Marshal cannot fail on strings, slices and a string-keyed map, and a
	// branch no input can take is one the mutation lane rightly calls dead.
	raw, _ := json.MarshalIndent(doc, "", " ")
	return append(raw, '\n')
}

// Die is the whole payload by file name: each star's reference-dialect
// orbit.toml and orbits.json.
func Die(contracts []Contract) map[string][]byte {
	files := map[string][]byte{DieIndex: Index(contracts)}
	for _, s := range Compose(contracts) {
		files[s.Star+SidecarSuffix] = RenderReference(s)
	}
	return files
}

// DieOwned reports whether name is a file the composer may write or delete
// in a die directory: a sidecar or the index. Anything else is a person's.
func DieOwned(name string, _ []byte) bool {
	return name == DieIndex || strings.HasSuffix(name, SidecarSuffix)
}
