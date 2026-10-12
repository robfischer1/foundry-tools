package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/checks"
)

// athenaEntry is a catalog entry, fleet/stars/athena.json.
const athenaEntry = `{"description":"Plans.","kind":"go","lifecycle":"production","name":"athena","repo":"rob/athena"}`

// The fleet die serves the catalog entries and the facts flux states about
// each known star, staged as the two data.json files opa build reads.
func TestTheRosterServesTheCatalogEntriesAndTheFluxFacts(t *testing.T) {
	m := bundleOn(t, map[string]string{"fleet/stars/athena.json": athenaEntry})
	scriptAGreenBundle()
	scriptTheFleetWith(strings.Replace(rosterData, `"declared":{}`, `"declared":{"athena":{}},"lean":{"ares":{}}`, 1))
	bundles(t, m)
	settledOn(t, "0", "published and signed")

	if engine.chain(`commit(id:"`+fakeFluxSHA+`")`, `glob(pattern:"prime/star-*.yaml")`) == "" {
		t.Error("flux was not read at the commit main resolved to")
	}
	stage := engine.chain(`path:"fleet/flux/data.json"`, `"fleet/flux_source/data.json"`, `"fleet/declared/data.json"`, `path:".manifest"`)
	if stage == "" {
		t.Fatal("the roster was not built from a stage carrying both injected files")
	}
	for _, want := range []string{
		`\"athena\": {`, `\"listen\": 8200`, `\"mtls\": 8201`, `\"aether-db\"`, // the flux facts
		`\"repo\": \"rob/athena\"`, `\"lifecycle\": \"production\"`, // the catalog entry
		`stars/*.json`,                     // the entries reach the die only through the stage
		`\"sha\": \"` + fakeFluxSHA + `\"`, // the flux commit the facts were read at
	} {
		if !strings.Contains(stage, want) {
			t.Errorf("the stage lacks %s:\n%s", want, stage)
		}
	}
	if strings.Contains(stage, "svid-sidecar") {
		t.Error("flux's svid-sidecar is not a star the tree knows, and got a flux row")
	}
}

// The entries live at fleet/repos/<name>.json (F10). Until foundry-dies lands
// the move they are still at fleet/stars/<name>.json, and the lane serves them
// from there; where both exist, fleet/repos/ is the whole catalog.
func TestTheCatalogEntriesAreReadFromReposThenFromStars(t *testing.T) {
	const aresEntry = `{"description":"Ares.","kind":"go","lifecycle":"production","name":"ares","repo":"rob/ares"}`
	for name, c := range map[string]struct {
		tree   map[string]string
		want   string
		absent string
	}{
		"fleet/repos":  {map[string]string{"fleet/repos/athena.json": athenaEntry}, `\"repo\": \"rob/athena\"`, ""},
		"fleet/stars":  {map[string]string{"fleet/stars/athena.json": athenaEntry}, `\"repo\": \"rob/athena\"`, ""},
		"repos wins":   {map[string]string{"fleet/repos/athena.json": athenaEntry, "fleet/stars/ares.json": aresEntry}, `\"repo\": \"rob/athena\"`, `rob/ares`},
		"repos stages": {map[string]string{"fleet/repos/athena.json": athenaEntry}, `repos/*.json`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			m := bundleOn(t, c.tree)
			scriptAGreenBundle()
			scriptTheFleetWith(strings.Replace(rosterData, `"declared":{}`, `"declared":{"athena":{}},"lean":{"ares":{}}`, 1))
			bundles(t, m)
			settledOn(t, "0", "published and signed")
			stage := engine.chain(`path:"fleet/declared/data.json"`, `path:".manifest"`)
			if !strings.Contains(stage, c.want) {
				t.Errorf("the stage lacks %s:\n%s", c.want, stage)
			}
			if c.absent != "" && strings.Contains(stage, c.absent) {
				t.Errorf("the stage carries %s:\n%s", c.absent, stage)
			}
		})
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
		{"the built roster names another flux", nil, func() {
			scriptTheFleetWith(strings.Replace(rosterData, fakeFluxSHA, strings.Repeat("0", 40), 1))
		}, "1", "does not name flux " + fakeFluxSHA},
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

// A read the engine could not answer is could-not-run, naming what it was
// reading — never an empty key.
func TestAnInjectionReadTheEngineLostCouldNotRun(t *testing.T) {
	for name, c := range map[string]struct{ needle, reason string }{
		"resolving flux main":      {`ref(name:"main"){commit}`, "foundry/flux main could not be resolved to a commit"},
		"the committed-file probe": {`glob(pattern:"fleet/declared/data.json")`, "fleet/declared/data.json could not be probed"},
		"the catalog entries":      {`glob(pattern:"fleet/repos/*.json")`, "fleet/repos/*.json could not be listed"},
		"the legacy entries":       {`glob(pattern:"fleet/stars/*.json")`, "fleet/stars/*.json could not be listed"},
		"flux's star manifests":    {`glob(pattern:"prime/star-*.yaml")`, "prime/star-*.yaml could not be listed"},
		"flux's data/":             {`glob(pattern:"data/*.yaml")`, "data/*.yaml could not be listed"},
		"one manifest's contents":  {`file(path:"prime/star-athena.yaml"){contents}`, "prime/star-athena.yaml could not be read"},
	} {
		t.Run(name, func(t *testing.T) {
			m := bundleOn(t, nil)
			scriptAGreenBundle()
			engine.fail(c.needle, "the engine lost it")
			bundles(t, m)
			settledOn(t, "2", c.reason)
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

// A tree that carries fleet/derive.rego (slag-by-kind F9) gets its lean rows
// staged at data.fleet.lean, out of the rule's path, and its derivation suite
// run beside the policy's.
func TestADerivingTreeStagesTheLeanRowsAsideAndTestsTheDerivation(t *testing.T) {
	m := bundleOn(t, map[string]string{
		checks.FleetDerive:             "package fleet",
		"fleet/stars/athena/data.json": `{"name":"athena"}`,
	})
	scriptAGreenBundle()
	said := sayings(t, func() { bundles(t, m) })
	settledOn(t, "0", "published and signed")

	stage := engine.chain(`"fleet/lean/athena/data.json"`, `path:".manifest"`)
	if stage == "" {
		t.Fatal("the lean row was not staged at fleet/lean/athena/data.json")
	}
	if !strings.Contains(stage, `stars/*/data.json`) {
		t.Errorf("the lean rows still reach fleet/stars in the stage:\n%s", stage)
	}
	if engine.chain(`"opa","test","policy/","fleet/derive.rego","tests/fleet-derive/","-v"`) == "" {
		t.Error("the policy re-gate did not test the derivation")
	}
	if engine.chain(`"opa","eval","-b","/work/fleet-bundle.tar.gz","--format","raw","data"`) == "" {
		t.Error("the roster was not graded as opa evaluates it")
	}
	if !strings.Contains(said, "1 lean rows staged at data.fleet.lean") {
		t.Errorf("the lane did not say where the lean rows went:\n%s", said)
	}
}

// Without the file the stage keeps the lean rows where they were.
func TestATreeWithoutTheDerivationKeepsItsLeanRowsInPlace(t *testing.T) {
	m := bundleOn(t, map[string]string{"fleet/stars/athena/data.json": `{"name":"athena"}`})
	scriptAGreenBundle()
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	if engine.chain(`"fleet/lean/`) != "" {
		t.Error("a tree that does not derive had its lean rows moved")
	}
	if engine.chain(`"opa","test","policy/","-v"`) == "" {
		t.Error("the policy suite did not run as before")
	}
}

func TestADerivingTreeRefusesWhatWouldHideTheRoster(t *testing.T) {
	derive := map[string]string{checks.FleetDerive: "package fleet"}
	t.Run("a committed fleet/lean", func(t *testing.T) {
		m := bundleOn(t, map[string]string{checks.FleetDerive: "package fleet", "fleet/lean/athena/data.json": "{}"})
		scriptAGreenBundle()
		bundles(t, m)
		settledOn(t, "1", "fleet/lean/ is committed")
		nothingPushed(t)
	})
	t.Run("a roster that does not evaluate", func(t *testing.T) {
		m := bundleOn(t, derive)
		scriptAGreenBundle()
		engine.exitCode(`"--format","raw","data"`, 1)
		bundles(t, m)
		settledOn(t, "1", "the built roster does not evaluate")
		nothingPushed(t)
	})
	for name, c := range map[string]struct{ needle, reason string }{
		"the derivation probe":  {`glob(pattern:"fleet/derive.rego")`, "fleet/derive.rego could not be probed"},
		"the fleet/lean probe":  {`glob(pattern:"fleet/lean/**")`, "fleet/lean/ could not be probed"},
		"the lean rows listing": {`glob(pattern:"fleet/stars/*/data.json")`, "the lean rows could not be listed"},
		"the roster evaluation": {`"--format","raw","data"`, "evaluating the built roster never ran"},
	} {
		t.Run(name, func(t *testing.T) {
			m := bundleOn(t, derive)
			scriptAGreenBundle()
			engine.fail(c.needle, "the engine lost it")
			bundles(t, m)
			settledOn(t, "2", c.reason)
			nothingPushed(t)
		})
	}
}
