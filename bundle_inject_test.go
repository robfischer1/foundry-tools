package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/bundlelane"
)

// athenaEntry is a catalog entry, fleet/stars/athena.json.
const athenaEntry = `{"description":"Plans.","kind":"go","lifecycle":"production","name":"athena","repo":"rob/athena"}`

// The fleet die serves the catalog entries and the facts flux states about
// each known star, staged as the two data.json files opa build reads.
func TestTheRosterServesTheCatalogEntriesAndTheFluxFacts(t *testing.T) {
	m := bundleOn(t, map[string]string{"fleet/stars/athena.json": athenaEntry})
	scriptAGreenBundle()
	scriptTheFleetWith(strings.Replace(rosterData, `"declared":{}`, `"declared":{"athena":{}}`, 1))
	bundles(t, m)
	settledOn(t, "0", "published and signed")

	stage := engine.chain(`path:"fleet/flux/data.json"`, `"fleet/declared/data.json"`, `path:".manifest"`)
	if stage == "" {
		t.Fatal("the roster was not built from a stage carrying both injected files")
	}
	for _, want := range []string{
		`\"athena\": {`, `\"listen\": 8200`, `\"mtls\": 8201`, `\"aether-db\"`, // the flux facts
		`\"repo\": \"rob/athena\"`, `\"lifecycle\": \"production\"`, // the catalog entry
		`stars/*.json`, // the entries reach the die only through the stage
	} {
		if !strings.Contains(stage, want) {
			t.Errorf("the stage lacks %s:\n%s", want, stage)
		}
	}
	if strings.Contains(stage, "svid-sidecar") {
		t.Error("flux's svid-sidecar is not a star the tree knows, and got a flux row")
	}
}

func TestTheInjectionRefusesWhatWouldServeTheWrongFacts(t *testing.T) {
	cases := []struct {
		name   string
		tree   map[string]string
		script func()
		code   string
		reason string
	}{
		{"the tier already sets an injected key", map[string]string{"fleet/data.json": `{"map":{"athena":{},"ares":{}},"flux":{}}`}, nil,
			"1", "fleet/data.json already sets data.fleet.flux"},
		{"an injected file is committed", map[string]string{"fleet/flux/data.json": "{}"}, nil,
			"1", "fleet/flux/data.json is committed"},
		{"an entry named for another star", map[string]string{"fleet/stars/athena.json": `{"name":"ares"}`}, nil,
			"1", `fleet/stars/athena.json: name must be \"athena\"`},
		{"an entry that is not JSON", map[string]string{"fleet/stars/athena.json": "{"}, nil,
			"1", "fleet/stars/athena.json is not one JSON object"},
		{"flux answers no star manifest", map[string]string{"/flux/prime/star-athena.yaml": "", "/flux/prime/star-svid-sidecar.yaml": ""}, nil,
			"1", "foundry/flux answered no prime/star-*.yaml"},
		{"a flux manifest that does not parse", map[string]string{"/flux/prime/star-athena.yaml": "kind: [unclosed"}, nil,
			"1", "foundry/flux prime/star-athena.yaml"},
		{"the built roster lost the flux facts", nil, func() {
			scriptTheFleetWith(strings.Replace(rosterData, `,"flux":{"athena":{}}`, "", 1))
		}, "1", "carries no data.fleet.flux"},
		{"the built roster lost a row", nil, func() {
			scriptTheFleetWith(strings.Replace(rosterData, `"flux":{"athena":{}}`, `"flux":{}`, 1))
		}, "1", "data.fleet.flux has 0 rows, the lane staged 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := bundleOn(t, c.tree)
			scriptAGreenBundle()
			if c.script != nil {
				c.script()
			}
			bundles(t, m)
			settledOn(t, c.code, c.reason)
			nothingPushed(t)
		})
	}
}

// The injected paths are the data roots the decision names.
func TestTheInjectedPathsAreDataFleetDeclaredAndFlux(t *testing.T) {
	if bundlelane.DeclaredPath != "fleet/declared/data.json" || bundlelane.FluxPath != "fleet/flux/data.json" {
		t.Fatalf("staged at %s and %s", bundlelane.DeclaredPath, bundlelane.FluxPath)
	}
}
