package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/fluxpin"
	"dagger/foundry-tools/internal/orbitcompose"
)

const (
	laneContract    = "version = \"1\"\nstatus = \"generated\"\nverbs = [\"neighbors\"]\n"
	themisRoster    = `{"name":"themis","verb_prefix":"themis"}`
	uraniaRoster    = `{"name":"urania","verb_prefix":"urania"}`
	narcSurfaceNone = `{"version":1,"findings":[]}`
)

// orbitAtom runs one orbit atom over the tree a test placed, as repo.
func orbitAtom(t *testing.T, id, repo string, tree map[string]string) checks.Verdict {
	t.Helper()
	engine.reset()
	engine.withTree(tree)
	return orbitAtomAgain(t, id, repo)
}

// orbitAtomAgain runs it again over the engine as scripted.
func orbitAtomAgain(t *testing.T, id, repo string) checks.Verdict {
	t.Helper()
	return registry[id](context.Background(), newRun(dag.Directory(), repo, ""))
}

// laneDies is foundry-dies at main: one contract and its two stars.
func laneDies(extra map[string]string) map[string]string {
	tree := map[string]string{
		"/dies/orbits/urania-themis.toml":    laneContract,
		"/dies/orbits/README.md":             "not a contract",
		"/dies/fleet/stars/urania/data.json": uraniaRoster,
		"/dies/fleet/stars/themis/data.json": themisRoster,
		"/dies/fleet/stars/broken/data.json": "not json",
		"/flux/" + fluxpin.File:              fluxPins,
	}
	for k, v := range extra {
		tree[k] = v
	}
	return tree
}

// contractsTree is foundry-dies itself under test.
func contractsTree(extra map[string]string) map[string]string {
	tree := laneDies(map[string]string{
		"orbits/urania-themis.toml":    laneContract,
		"orbits/README.md":             "the contracts",
		"fleet/stars/urania/data.json": uraniaRoster,
		"fleet/stars/themis/data.json": themisRoster,
	})
	for k, v := range extra {
		tree[k] = v
	}
	return tree
}

// wantSaid is wantState over what the atom printed: a report-only atom passes,
// and a pass's reason is only its name, so what it found is in its lines and
// its findings.
func wantSaid(t *testing.T, v checks.Verdict, state int, needles ...string) {
	t.Helper()
	wantState(t, v, state)
	said := strings.Join(v.Logs, "\n")
	for _, n := range needles {
		if !strings.Contains(said, n) {
			t.Errorf("%s: lines lack %q:\n%s", v.Atom, n, said)
		}
	}
}

func findingsOf(v checks.Verdict) string {
	var out []string
	for _, f := range v.Findings {
		out = append(out, f.Verdict+" "+f.Cause+" "+f.Subject)
	}
	return strings.Join(out, "\n")
}

func TestOrbitAtomsAreTheOrbitStageOnly(t *testing.T) {
	for _, id := range []string{"orbit:contracts", "orbit:surface", "orbit:sidecars", "orbit:repo"} {
		a := checks.AtomByID(id)
		if a.Stage != checks.StageOrbit {
			t.Errorf("%s stage %q", id, a.Stage)
		}
		for _, p := range checks.PullPathAtoms() {
			if p.ID == id {
				t.Errorf("%s is in the gate's vector", id)
			}
		}
	}
	if laneOf(checks.StageOrbit) != "orbit" {
		t.Errorf("the orbit stage's record is labelled %q", laneOf(checks.StageOrbit))
	}
}

func TestOrbitContractsHoldsOnTheContractsTree(t *testing.T) {
	v := orbitAtom(t, "orbit:contracts", "foundry-dies", contractsTree(nil))
	wantSaid(t, v, 0, "orbit:contracts: 1 holds")
	if findingsOf(v) != "holds contract orbits/urania-themis.toml" || v.Findings[0].Probe != "orbit:contracts" {
		t.Errorf("findings %s", findingsOf(v))
	}
}

func TestOrbitContractsReportsABadContractWithoutFailing(t *testing.T) {
	v := orbitAtom(t, "orbit:contracts", "foundry-dies", contractsTree(map[string]string{
		"orbits/urania-ghost.toml": laneContract,
	}))
	wantSaid(t, v, 0, "1 drifted", "[report-only: would be violated] ghost is not on the fleet roster")
	if !strings.Contains(findingsOf(v), "drifted party-not-on-roster orbits/urania-ghost.toml") {
		t.Errorf("findings %s", findingsOf(v))
	}
}

// A hyphenated contract on the contracts tree reads against its own roster.
func TestOrbitContractsHoldsAHyphenatedStarOnTheRoster(t *testing.T) {
	v := orbitAtom(t, "orbit:contracts", "foundry-dies", contractsTree(map[string]string{
		"orbits/blade-runner-themis.toml":    laneContract,
		"fleet/stars/blade-runner/data.json": `{"name":"blade-runner","verb_prefix":"blade"}`,
	}))
	if got := findingsOf(v); !strings.Contains(got, "holds contract orbits/blade-runner-themis.toml") {
		t.Errorf("findings %s", got)
	}
}

// The base is foundry-dies main: an approved contract whose verbs moved
// there without a version bump is the finding.
func TestOrbitContractsReadsTheBaseFromTheDies(t *testing.T) {
	approved := strings.Replace(laneContract, "generated", "approved", 1)
	v := orbitAtom(t, "orbit:contracts", "foundry-dies", contractsTree(map[string]string{
		"/dies/orbits/urania-themis.toml": approved,
		"orbits/urania-themis.toml":       strings.Replace(approved, `"neighbors"`, `"shapes"`, 1),
	}))
	wantSaid(t, v, 0, "raise version in the same pull")
}

func TestOrbitContractsIsAbsentOutsideTheContractsTree(t *testing.T) {
	for name, tree := range map[string]map[string]string{
		"a star":         laneDies(map[string]string{"go.mod": "module x"}),
		"orbits alone":   laneDies(map[string]string{"orbits/urania-themis.toml": laneContract}),
		"a roster alone": laneDies(map[string]string{"fleet/stars/urania/data.json": uraniaRoster}),
	} {
		v := orbitAtom(t, "orbit:contracts", "themis", tree)
		wantState(t, v, 0, "orbit:contracts: ABSENT - this tree is not the contracts' repository")

		if v.Result != "absent" {
			t.Errorf("%s: result %q", name, v.Result)
		}
	}
}

func TestOrbitContractsCannotRunWhenATreeCannotBeRead(t *testing.T) {
	for name, tc := range map[string]struct{ needle, leaf, want string }{
		"the contracts":  {`pattern:"orbits/*"`, "glob", "orbits/* could not be read"},
		"a contract":     {`"orbits/urania-themis.toml"`, "contents", "could not be read"},
		"the roster":     {`"fleet/stars/*/data.json"`, "", "the roster could not be listed"},
		"a roster shard": {`"fleet/stars/themis/data.json"`, "contents", "could not be read"},
	} {
		t.Run(name, func(t *testing.T) {
			engine.reset()
			engine.withTree(contractsTree(nil))
			engine.failLeaf(tc.needle, tc.leaf, "engine went away")
			wantState(t, orbitAtomAgain(t, "orbit:contracts", "foundry-dies"), 2, tc.want)
		})
	}
}

func TestOrbitContractsCannotRunWhenTheDiesCannotBeRead(t *testing.T) {
	engine.reset()
	engine.withTree(contractsTree(nil))
	engine.failLeaf(`"/dies/orbits/urania-themis.toml"`, "contents", "engine went away")
	engine.failLeaf(`url:"`, "contents", "engine went away")
	wantState(t, orbitAtomAgain(t, "orbit:contracts", "foundry-dies"), 2, "foundry-dies main:")
}

func TestOrbitRepoJudgesTheLaidFile(t *testing.T) {
	cs, err := orbitcompose.ContractsOf(map[string][]byte{"urania-themis.toml": []byte(laneContract)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	laid := string(orbitcompose.RenderReference(orbitcompose.Compose(cs)[0]))
	v := orbitAtom(t, "orbit:repo", "http://door/rob/themis.git", laneDies(map[string]string{"orbit.toml": laid}))
	wantSaid(t, v, 0, "orbit:repo: 1 holds")
	if findingsOf(v) != "holds edge consumes urania" {
		t.Errorf("findings %s", findingsOf(v))
	}
	stale := strings.Replace(laid, "sha256:", "sha256:0", 1)
	v = orbitAtom(t, "orbit:repo", "http://door/rob/themis.git", laneDies(map[string]string{"orbit.toml": stale}))
	wantSaid(t, v, 0, "1 drifted", "the contract moved and this file did not")
}

// ONE BAD CONTRACT IN foundry-dies IS A FINDING ON THAT FILE in a star's own
// lane, and the star is still judged against the contracts that read.
func TestOrbitRepoReportsAnUnreadableContractAndJudgesTheRest(t *testing.T) {
	laid := string(orbitcompose.RenderReference(orbitcompose.Compose(mustContracts(t, "urania-themis.toml"))[0]))
	v := orbitAtom(t, "orbit:repo", "themis", laneDies(map[string]string{
		"orbit.toml":                      laid,
		"/dies/orbits/broken-themis.toml": "version = 1",
	}))
	wantState(t, v, 0)
	got := findingsOf(v)
	if !strings.Contains(got, "holds edge consumes urania") || !strings.Contains(got, "drifted contract-unparseable orbits/broken-themis.toml") {
		t.Errorf("findings %s", got)
	}
}

// A HYPHENATED STAR IS ONE PARTY, split against foundry-dies' own roster.
func TestOrbitRepoSplitsAHyphenatedStarAgainstTheDiesRoster(t *testing.T) {
	v := orbitAtom(t, "orbit:repo", "themis", laneDies(map[string]string{
		"orbit.toml":                               "# x\n",
		"/dies/orbits/blade-runner-themis.toml":    laneContract,
		"/dies/fleet/stars/blade-runner/data.json": `{"name":"blade-runner","verb_prefix":"blade"}`,
	}))
	if got := findingsOf(v); !strings.Contains(got, "consumes blade-runner") || strings.Contains(got, "contract-unparseable") {
		t.Errorf("findings %s", got)
	}
}

func TestOrbitRepoCannotRunWhenTheRosterCannotBeListed(t *testing.T) {
	engine.reset()
	engine.withTree(laneDies(map[string]string{"orbit.toml": "# x\n"}))
	engine.failLeaf(`pattern:"fleet/stars/*/data.json"`, "glob", "engine went away")
	wantState(t, orbitAtomAgain(t, "orbit:repo", "themis"), 2, "foundry-dies: the roster could not be listed")
}

func TestStarSetIsTheRostersNamesOrNilForNone(t *testing.T) {
	if starSet(nil) != nil || starSet(map[string]string{}) != nil {
		t.Error("an empty roster must be nil, the two-part rule alone")
	}
	got := starSet(map[string]string{"blade-runner": "blade", "themis": ""})
	if len(got) != 2 || !got["blade-runner"] || !got["themis"] {
		t.Errorf("got %v", got)
	}
}

func mustContracts(t *testing.T, names ...string) []orbitcompose.Contract {
	t.Helper()
	files := map[string][]byte{}
	for _, n := range names {
		files[n] = []byte(laneContract)
	}
	cs, err := orbitcompose.ContractsOf(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestOrbitRepoIsAbsentWithoutALaidFile(t *testing.T) {
	v := orbitAtom(t, "orbit:repo", "themis", laneDies(nil))
	wantState(t, v, 0, "orbit:repo: ABSENT - this repository carries no root orbit.toml")

}

func TestOrbitRepoCannotRunWithoutTheContracts(t *testing.T) {
	tree := laneDies(map[string]string{"orbit.toml": "# x\n"})
	delete(tree, "/dies/orbits/urania-themis.toml")
	wantState(t, orbitAtom(t, "orbit:repo", "themis", tree), 2, "foundry-dies/orbits does not compose")

	engine.reset()
	engine.withTree(laneDies(map[string]string{"orbit.toml": "# x\n"}))
	engine.failLeaf(`pattern:"orbits/*.toml"`, "glob", "engine went away")
	wantState(t, orbitAtomAgain(t, "orbit:repo", "themis"), 2, "foundry-dies: orbits/*.toml could not be read")

	engine.reset()
	engine.withTree(laneDies(map[string]string{"orbit.toml": "# x\n"}))
	engine.failLeaf(`pattern:"orbit.toml"`, "glob", "engine went away")
	wantState(t, orbitAtomAgain(t, "orbit:repo", "themis"), 2, "CANNOT RUN")
}

// narc answers a scan by name.
func scriptNarc(surface, orbits string) {
	engine.stdout(`"narc","scan","surface"`, surface)
	engine.stdout(`"narc","scan","orbits"`, orbits)
}

// narcissus as flux pins it live.
const (
	narcDigest = "sha256:0774e298540cdf764d5ed20e57241db84ea1af9c09f8aac16219bed017a05921"
	fluxPins   = "apiVersion: kustomize.config.k8s.io/v1alpha1\nkind: Component\nimages:\n" +
		"  - name: " + NarcissusImage + "\n    newTag: 1791061288-a2c1393 # {\"$imagepolicy\": \"flux-system:narcissus:tag\"}\n" +
		"    digest: " + narcDigest + " # {\"$imagepolicy\": \"flux-system:narcissus:digest\"}\n"
)

func TestOrbitSurfaceRunsNarcissusOverTheCheckout(t *testing.T) {
	engine.reset()
	engine.withTree(laneDies(map[string]string{"go.mod": "module themis"}))
	scriptNarc(narcSurfaceNone, `{"version":1,"findings":[`+
		`{"verdict":"unanalyzable","subject":"/src/a.go:1","cause":"loose-verb","detail":"urania_neighbors — via the gateway"},`+
		`{"verdict":"unanalyzable","subject":"/src/b.go:2","cause":"loose-verb","detail":"urania_shapes — not contracted"}]}`)
	v := orbitAtomAgain(t, "orbit:surface", "http://door/rob/themis.git")
	wantSaid(t, v, 0, "1 unanalyzable, 1 holds")
	chain := engine.chain(`"narc","scan","surface"`)
	if !hasCall(chain, "withFile", `"/usr/local/bin/narc"`) || !strings.Contains(chain, `"narc","scan","surface","--json","-","/src"`) || !strings.Contains(chain, "expect:ANY") {
		t.Errorf("narc was not run over /src with any exit:\n%s", chain)
	}
	if engine.chain(`"`+NarcissusImage+`:1791061288-a2c1393@`+narcDigest+`"`, `"/narc"`) == "" {
		t.Error("narc did not come from narcissus's image at the tag and digest flux pins")
	}
	if engine.chain(`narcissus:stable`) != "" {
		t.Error("narc was pulled from the frozen :stable tag")
	}
	if engine.chain(`"narc","scan","orbits"`) == "" {
		t.Error("the dials were never read")
	}
}

// The check Rob asked for: a star that dials a verb its contract does not name
// is told which file to edit and what to add.
func TestOrbitSurfaceTeachesTheContractToEdit(t *testing.T) {
	engine.reset()
	engine.withTree(laneDies(map[string]string{
		"go.mod":                             "module themis",
		"/dies/orbits/urania-athena.toml":    strings.Replace(laneContract, `"neighbors"`, `"neighbors", "shapes"`, 1),
		"/dies/fleet/stars/athena/data.json": `{"verb_prefix":"tasks"}`,
	}))
	scriptNarc(narcSurfaceNone, `{"version":1,"findings":[{"verdict":"unanalyzable","subject":"/src/b.go:2","cause":"loose-verb","detail":"urania_shapes — new call"}]}`)
	v := orbitAtomAgain(t, "orbit:surface", "http://door/rob/themis.git")
	wantSaid(t, v, 0, `themis dials urania_shapes, which urania serves, and orbits/urania-themis.toml does not name it — add "shapes"`)
}

func TestOrbitSurfaceIsAbsentWhereThereIsNoSeamToCheck(t *testing.T) {
	v := orbitAtom(t, "orbit:surface", "foundry-dies", contractsTree(nil))
	wantState(t, v, 0, "orbit:surface: ABSENT - the contracts' repository")

	engine.reset()
	engine.withTree(laneDies(nil))
	scriptNarc(narcSurfaceNone, narcSurfaceNone)
	v = orbitAtomAgain(t, "orbit:surface", "http://door/rob/urania.git")
	wantSaid(t, v, 0, "urania registers no neighbors")
	v = orbitAtom(t, "orbit:surface", "http://door/rob/eros.git", laneDies(nil))
	wantState(t, v, 0, "orbit:surface: ABSENT - eros takes part in no contracted seam")

}

func TestOrbitSurfaceCannotRunWhenItCannotLook(t *testing.T) {
	for name, tc := range map[string]struct {
		script func()
		want   string
	}{
		"the tree":      {func() { engine.failLeaf(`pattern:"orbits/*"`, "glob", "gone") }, "orbits/* could not be read"},
		"the contracts": {func() { engine.failLeaf(`pattern:"orbits/*.toml"`, "glob", "gone") }, "foundry-dies: orbits/*.toml could not be read"},
		"the roster":    {func() { engine.failLeaf(`pattern:"fleet/stars/*/data.json"`, "glob", "gone") }, "foundry-dies: the roster could not be listed"},
		"a narc scan":   {func() { engine.failLeaf(`"narc","scan","orbits"`, "stdout", "gone") }, "narc scan orbits never ran"},
		"a narc answer": {func() { scriptNarc("a table", narcSurfaceNone) }, "narc scan surface answered something that is not its report"},
	} {
		t.Run(name, func(t *testing.T) {
			engine.reset()
			engine.withTree(laneDies(map[string]string{"go.mod": "module themis"}))
			scriptNarc(narcSurfaceNone, narcSurfaceNone)
			tc.script()
			wantState(t, orbitAtomAgain(t, "orbit:surface", "http://door/rob/themis.git"), 2, tc.want)
		})
	}
	tree := laneDies(map[string]string{"go.mod": "module themis"})
	delete(tree, "/dies/orbits/urania-themis.toml")
	wantState(t, orbitAtom(t, "orbit:surface", "http://door/rob/themis.git", tree), 2, "does not compose")
}

func TestOrbitSidecarsFoldsTheTwoFluxChecks(t *testing.T) {
	composedTree(t, nil)
	v := orbitAtomAgain(t, "orbit:sidecars", "flux")
	wantSaid(t, v, 0, "orbit:sidecars: 2 holds")
	if findingsOf(v) != "holds ops:orbit-composed prime/orbits\nholds ops:orbit-sidecars prime/orbits" {
		t.Errorf("findings %s", findingsOf(v))
	}
	composedTree(t, func(r map[string][]byte) { r["themis.orbit.toml"] = []byte("stale") })
	v = orbitAtomAgain(t, "orbit:sidecars", "flux")
	wantSaid(t, v, 0, "1 drifted", "[report-only: would be violated]", "stale against its contracts")
}

func TestOrbitSidecarsIsAbsentWithoutARender(t *testing.T) {
	wantState(t, orbitAtom(t, "orbit:sidecars", "themis", laneDies(nil)), 0, "orbit:sidecars: ABSENT - this tree renders no orbit sidecars")
}

func TestOrbitSidecarsCannotRunWhenACheckCannot(t *testing.T) {
	composedTree(t, nil)
	engine.failLeaf(`pattern:"orbits/*.toml"`, "glob", "gone")
	wantState(t, orbitAtomAgain(t, "orbit:sidecars", "flux"), 2, "ops:orbit-composed could not run")

	composedTree(t, nil)
	engine.failLeaf(`pattern:"prime/orbits/*.orbit.toml"`, "glob", "gone")
	wantState(t, orbitAtomAgain(t, "orbit:sidecars", "flux"), 2, "CANNOT RUN")
}

// No pin, no run: an unreadable pin or one with no narcissus entry settles the
// atom COULD-NOT-RUN naming the file and why, and never reaches narc.
func TestOrbitSurfaceCannotRunWithoutAFluxPin(t *testing.T) {
	for name, tc := range map[string]struct {
		pins, why string
	}{
		"no entry":  {"images: []\n", "has no entry for " + NarcissusImage},
		"malformed": {strings.Replace(fluxPins, narcDigest, "sha256:zz", 1), "malformed digest"},
	} {
		engine.reset()
		engine.withTree(laneDies(map[string]string{"go.mod": "module themis", "/flux/" + fluxpin.File: tc.pins}))
		scriptNarc(narcSurfaceNone, narcSurfaceNone)
		v := orbitAtomAgain(t, "orbit:surface", "http://door/rob/themis.git")
		wantState(t, v, 2, "CANNOT RUN", fluxpin.File, tc.why)
		if engine.chain(`"narc","scan"`) != "" {
			t.Errorf("%s: narc ran without a pin", name)
		}
	}
	// The file is gone from a flux tree that is otherwise there.
	tree := laneDies(map[string]string{"go.mod": "module themis", "/flux/README.md": "x"})
	delete(tree, "/flux/"+fluxpin.File)
	engine.reset()
	engine.withTree(tree)
	scriptNarc(narcSurfaceNone, narcSurfaceNone)
	wantState(t, orbitAtomAgain(t, "orbit:surface", "http://door/rob/themis.git"), 2,
		"CANNOT RUN", fluxpin.File, "could not be read")
}
