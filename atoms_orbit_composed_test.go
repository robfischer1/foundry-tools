package main

import (
	"testing"

	"dagger/foundry-tools/internal/orbitcompose"
)

const composedContract = `version = "1"
status = "generated"
wire_form = "native"
verbs = ["shape_for"]

[witness]
calls = 1
verbs = ["shape_for"]
`

// composedTree places foundry-dies' contracts under the dies' mount and the
// tree's render under prime/orbits — the render a correct orbitcompose run
// would write, edited by edit.
func composedTree(t *testing.T, edit func(map[string][]byte)) {
	t.Helper()
	contracts := map[string][]byte{"urania-themis.toml": []byte(composedContract)}
	cs, err := orbitcompose.ContractsOf(contracts)
	if err != nil {
		t.Fatal(err)
	}
	render := orbitcompose.Files(orbitNamespace, orbitcompose.Compose(cs))
	if edit != nil {
		edit(render)
	}
	tree := map[string]string{
		"/dies/orbits/urania-themis.toml": composedContract,
		"/dies/orbits/README.md":          "not read",
	}
	for name, raw := range render {
		if raw == nil {
			tree["prime/orbits/"+name] = ""
			continue
		}
		tree["prime/orbits/"+name] = string(raw)
	}
	engine.reset()
	engine.withTree(tree)
}

// A RENDER THAT IS THE COMPOSED SET OF THE DIES' CONTRACTS PASSES.
func TestOrbitComposedPassesAnUpToDateRender(t *testing.T) {
	composedTree(t, nil)
	v := runAtom(t, "ops:orbit-composed", "")
	wantState(t, v, 0)
	if v.Result != "pass" || len(v.Logs) == 0 || v.Logs[0] != "prime/orbits against foundry-dies main: the directory is the composed set of 1 contract(s)" {
		t.Fatalf("want a pass that says what it compared: %+v", v)
	}
}

// A STALE RENDER FAILS LOUDLY, naming each file a re-render would touch and
// how to re-render.
func TestOrbitComposedFindsAStaleRender(t *testing.T) {
	composedTree(t, func(r map[string][]byte) {
		r["themis.orbit.toml"] = []byte("edited by hand\n")
		r["nyx.orbit.toml"] = []byte("a star whose contracts went\n")
	})
	wantState(t, runAtom(t, "ops:orbit-composed", ""), 1,
		"write  themis.orbit.toml", "remove nyx.orbit.toml", orbitcompose.RenderHint)
}

// A DIRECTORY INSIDE THE RENDER IS NAMED AS A FILE THE COMPOSER DOES NOT
// OWN, never read as one.
func TestOrbitComposedNamesASubdirectory(t *testing.T) {
	composedTree(t, func(r map[string][]byte) { r["sub/"] = nil })
	wantState(t, runAtom(t, "ops:orbit-composed", ""), 1, "sub is not the composer's")
}

// A TREE THAT RENDERS NO SIDECARS IS ABSENT, and the dies are never read.
func TestOrbitComposedIsAbsentWithoutARender(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n"})
	v := runAtom(t, "ops:orbit-composed", "")
	if v.State != 0 || v.Result != "absent" {
		t.Fatalf("want absent: %+v", v)
	}
	if engine.chain(`"orbits/*.toml"`) != "" {
		t.Errorf("the dies were read for a tree with nothing to compare:\n%s", engine.chain(`"orbits/*.toml"`))
	}
}

// WHAT CANNOT BE READ CANNOT BE JUDGED: either directory failing is a
// could-not-run that says which.
func TestOrbitComposedCannotRunWhenADirectoryCannotBeRead(t *testing.T) {
	composedTree(t, nil)
	engine.fail(`"prime/orbits/*"`, "engine went away")
	wantState(t, runAtom(t, "ops:orbit-composed", ""), 2, "the rendered sidecars could not be read", "engine went away")

	composedTree(t, nil)
	engine.fail(`"orbits/*.toml"`, "the dies went away")
	wantState(t, runAtom(t, "ops:orbit-composed", ""), 2, "foundry-dies' contracts could not be read", "the dies went away")

	composedTree(t, nil)
	engine.fail(`path:"prime/orbits/kustomization.yaml"`, "read refused")
	wantState(t, runAtom(t, "ops:orbit-composed", ""), 2, "the rendered sidecars could not be read", "read refused")
}

// CONTRACTS THAT DO NOT COMPOSE ARE A COULD-NOT-RUN, not a finding against
// the render.
func TestOrbitComposedCannotRunOnBrokenContracts(t *testing.T) {
	composedTree(t, nil)
	engine.withTree(map[string]string{
		"/dies/orbits/urania-themis.toml": "version = 1",
		"prime/orbits/kustomization.yaml": orbitcompose.OwnerMark + "\n",
	})
	wantState(t, runAtom(t, "ops:orbit-composed", ""), 2, "the contracts do not compose")
}
