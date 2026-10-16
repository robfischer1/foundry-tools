package opsorbitcomposed

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitcompose"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "prepush", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

// A RENDER THAT IS THE COMPOSED SET OF THE DIES' CONTRACTS PASSES.
func TestOrbitComposedPassesAnUpToDateRender(t *testing.T) {
	v := run(atomtest.ComposedTree(t, nil, nil))
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass)
	if v.Result != "pass" || len(v.Logs) == 0 || v.Logs[0] != "prime/orbits against foundry-dies main: the directory is the composed set of 1 contract(s)" {
		t.Fatalf("want a pass that says what it compared: %+v", v)
	}
}

// A STALE RENDER FAILS LOUDLY, naming each file a re-render would touch and how
// to re-render.
func TestOrbitComposedFindsAStaleRender(t *testing.T) {
	in := atomtest.ComposedTree(t, func(r map[string][]byte) {
		r["themis.orbit.toml"] = []byte("edited by hand\n")
		r["nyx.orbit.toml"] = []byte("a star whose contracts went\n")
	}, nil)
	atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings,
		"write  themis.orbit.toml", "remove nyx.orbit.toml", orbitcompose.RenderHint)
}

// A rendered sidecar the contracts do not compose is drift.
func TestOrbitComposedFindsADriftedSidecar(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x"})
	in.Dies = atomtest.DiesAt(t, nil)
	atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "prime/orbits against foundry-dies main: ")
}

// A DIRECTORY INSIDE THE RENDER IS NAMED AS A FILE THE COMPOSER DOES NOT OWN,
// never read as one.
func TestOrbitComposedNamesASubdirectory(t *testing.T) {
	in := atomtest.ComposedTree(t, nil, map[string]string{"prime/orbits/sub/x": "x"})
	v := run(in)
	if v.State != 1 || !strings.Contains(v.Reason, "sub") {
		t.Errorf("a directory among the rendered files is a file the composer does not own: state %d\n%s", v.State, v.Reason)
	}
	atomtest.Expect(t, v, atomtest.StateOf(1), atomtest.Findings, "sub is not the composer's")
}

// A TREE THAT RENDERS NO SIDECARS IS ABSENT, and the dies are never read.
func TestOrbitComposedIsAbsentWithoutARender(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{"go.mod": "module x\n"})
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Absent, "renders no orbit sidecars (no prime/orbits/)")
}

// Without foundry-dies main nothing can be compared: a contract nobody read
// agrees with nothing.
func TestOrbitComposedWithoutTheDiesIsATwo(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x"})
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "foundry-dies' contracts could not be read", "(-dies)")
}

// WHAT CANNOT BE READ CANNOT BE JUDGED: either tree failing is a could-not-run
// that says which.
func TestOrbitComposedCannotRunWhenADirectoryCannotBeRead(t *testing.T) {
	for name, tc := range map[string]struct {
		in     func(t *testing.T) atoms.Input
		needle string
	}{
		"a rendered sidecar that will not read": {func(t *testing.T) atoms.Input {
			in := atomtest.TreeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x"})
			atomtest.BrokenLink(t, in.Root, "prime/orbits/z.orbit.toml")
			in.Dies = atomtest.DiesAt(t, nil)
			return in
		}, "the rendered sidecars could not be read"},
		"foundry-dies' contracts that will not read": {func(t *testing.T) atoms.Input {
			in := atomtest.TreeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x"})
			in.Dies = atomtest.DiesAt(t, nil)
			atomtest.BrokenLink(t, in.Dies, "orbits/z.toml")
			return in
		}, "foundry-dies' contracts could not be read"},
		"foundry-dies' roster that will not read": {func(t *testing.T) atoms.Input {
			in := atomtest.TreeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x"})
			in.Dies = atomtest.DiesAt(t, nil)
			atomtest.BrokenLink(t, in.Dies, "fleet/repos/z.json")
			return in
		}, "foundry-dies' roster could not be read"},
	} {
		t.Run(name, func(t *testing.T) {
			atomtest.Expect(t, run(tc.in(t)), atomtest.StateOf(2), atomtest.Cannot, tc.needle)
		})
	}
}

// A CONTRACT THAT DOES NOT PARSE IS A FINDING ON THAT FILE, not a could-not-run:
// the readable contracts are still compared with the render.
func TestOrbitComposedReportsABrokenContractAsAFinding(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{"prime/orbits/kustomization.yaml": orbitcompose.OwnerMark + "\n"})
	in.Dies = atomtest.TreeIn(t, map[string]string{"orbits/urania-themis.toml": "version = 1"}).Root
	atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "contract-unparseable: urania-themis.toml")
}

// A HYPHENATED STAR IS COMPOSED WHEN foundry-dies' ROSTER NAMES IT.
func TestOrbitComposedSplitsHyphenatedStarsAgainstTheDiesRoster(t *testing.T) {
	stars := map[string]bool{"blade-runner": true, "themis": true}
	cs, err := orbitcompose.ContractsOf(map[string][]byte{"blade-runner-themis.toml": []byte(atomtest.ComposedContract)}, stars)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for name, raw := range orbitcompose.Files("prime", orbitcompose.Compose(cs)) {
		files["prime/orbits/"+name] = string(raw)
	}
	in := atomtest.TreeIn(t, files)
	in.Dies = atomtest.TreeIn(t, map[string]string{
		"orbits/blade-runner-themis.toml": atomtest.ComposedContract,
		"fleet/repos/blade-runner.json":   "{}",
		"fleet/repos/themis.json":         "{}",
	}).Root
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass)
}
