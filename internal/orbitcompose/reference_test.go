package orbitcompose

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestRenderReferencePinsEachEdgeAndCarriesNoVerbs(t *testing.T) {
	ut := mustParse(t, "urania-themis.toml", contractBody)
	cu := mustParse(t, "chaos-urania.toml", strings.Replace(contractBody, `version = "1"`, `version = "2"`, 1))
	var urania Sidecar
	for _, s := range Compose([]Contract{ut, cu}) {
		if s.Star == "urania" {
			urania = s
		}
	}
	raw := string(RenderReference(urania))
	want := "\n[[produces]]\nto = \"themis\"\ncontract = \"urania-themis\"\nversion = \"1\"\ndigest = \"" + ut.Digest + "\"\n" +
		"\n[[consumes]]\nfrom = \"chaos\"\ncontract = \"chaos-urania\"\nversion = \"2\"\ndigest = \"" + cu.Digest + "\"\n"
	if !strings.HasSuffix(raw, want) {
		t.Errorf("edges:\n%s\nwant suffix:\n%s", raw, want)
	}
	header := "# orbit.toml — urania's seams, by reference. Laid from the data/orbits die\n" +
		"# (foundry-tools orbitcompose over foundry-dies/orbits). Generated — do not\n" +
		"# hand-edit: a local edit is drift, and the next lay overwrites it.\n" +
		"#\n" +
		"# Each edge names a contract, foundry-dies/orbits/<contract>.toml, by name,\n" +
		"# version and sha256 of its bytes. The verbs live in the contract.\n" +
		"# [[produces]] is who breaks if urania changes what it SERVES; [[consumes]] is\n" +
		"# what urania breaks if it changes how it CALLS. To change a seam, edit the\n" +
		"# contract in a foundry-dies pull (orbits/README.md says how), then re-lay.\n"
	if raw != header+want {
		t.Errorf("got:\n%s\nwant:\n%s", raw, header+want)
	}
	if strings.Contains(raw, "\nverbs =") || strings.Contains(raw, "\nstatus =") || strings.Contains(raw, "\nwire_form =") {
		t.Errorf("a reference restates the contract's body:\n%s", raw)
	}
	var doc struct {
		Produces []map[string]string `toml:"produces"`
		Consumes []map[string]string `toml:"consumes"`
	}
	if _, err := toml.Decode(raw, &doc); err != nil || len(doc.Produces) != 1 || len(doc.Consumes) != 1 || doc.Consumes[0]["digest"] != cu.Digest {
		t.Errorf("does not decode as two edges: %+v %v", doc, err)
	}
}
