package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/checks"
)

// athenaEntry and aresEntry are catalog entries, fleet/repos/athena.json and
// fleet/repos/ares.json.
const (
	athenaEntry = `{"description":"Plans.","kind":"go","lifecycle":"production","name":"athena","repo":"rob/athena"}`
	aresEntry   = `{"description":"The model gateway.","kind":"go","lifecycle":"production","name":"ares","repo":"rob/ares"}`
)

// The fleet die serves the catalog entries and the facts flux states about
// each known star, staged as the two data.json files opa build reads.
func TestTheRosterServesTheCatalogEntriesAndTheFluxFacts(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	scriptTheFleetWith(rosterData)
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
		`repos/*.json`,                     // the entries reach the die only through the stage
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

// The entries live at fleet/repos/<name>.json (F10), and reach the die only
// through the staged declared/data.json.
func TestTheCatalogEntriesAreReadFromRepos(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	scriptTheFleetWith(rosterData)
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	stage := engine.chain(`path:"fleet/declared/data.json"`, `path:".manifest"`)
	for _, want := range []string{`\"repo\": \"rob/athena\"`, `\"repo\": \"rob/ares\"`, `repos/*.json`} {
		if !strings.Contains(stage, want) {
			t.Errorf("the stage lacks %s:\n%s", want, stage)
		}
	}
	if !strings.Contains(stage, `stars/**`) {
		t.Errorf("a tree that still carries fleet/stars/ is not excluded from the stage:\n%s", stage)
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
		{"an entry named for another star", map[string]string{"fleet/repos/athena.json": `{"name":"ares"}`}, nil,
			"1", `fleet/repos/athena.json: name must be \"athena\"`},
		{"an entry that is not JSON", map[string]string{"fleet/repos/athena.json": "{"}, nil,
			"1", "fleet/repos/athena.json is not one JSON object"},
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
		"flux's star manifests":    {`glob(pattern:"prime/star-*.yaml")`, "prime/star-*.yaml could not be listed"},
		"flux's data/":             {`glob(pattern:"data/*.yaml")`, "data/*.yaml could not be listed"},
		"flux's blades/":           {`glob(pattern:"blades/*.yaml")`, "blades/*.yaml could not be listed"},
		"flux's hemera/":           {`glob(pattern:"hemera/*.yaml")`, "hemera/*.yaml could not be listed"},
		"flux's Kustomizations":    {`glob(pattern:"clusters/pantheon/*.yaml")`, "clusters/pantheon/*.yaml could not be listed"},
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

// A tree that carries fleet/derive.rego (slag-by-kind F9) has its derivation
// suite run beside the policy's, and the roster graded as opa evaluates it.
func TestADerivingTreeTestsTheDerivation(t *testing.T) {
	m := bundleOn(t, map[string]string{checks.FleetDerive: "package fleet"})
	scriptAGreenBundle()
	said := sayings(t, func() { bundles(t, m) })
	settledOn(t, "0", "published and signed")

	if engine.chain(`"opa","test","policy/","fleet/derive.rego","tests/fleet-derive/","-v"`) == "" {
		t.Error("the policy re-gate did not test the derivation")
	}
	if engine.chain(`"opa","eval","-b","/work/fleet-bundle.tar.gz","--format","raw","data"`) == "" {
		t.Error("the roster was not graded as opa evaluates it")
	}
	if !strings.Contains(said, "derives data.fleet.stars and data.fleet.repos") {
		t.Errorf("the lane did not say the roster is derived:\n%s", said)
	}
}

// Without the file the policy suite runs as before.
func TestATreeWithoutTheDerivationRunsThePolicySuiteAlone(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	if engine.chain(`"opa","test","policy/","-v"`) == "" {
		t.Error("the policy suite did not run as before")
	}
}

func TestADerivingTreeRefusesWhatWouldHideTheRoster(t *testing.T) {
	derive := map[string]string{checks.FleetDerive: "package fleet"}
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
