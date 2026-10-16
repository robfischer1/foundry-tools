package orbitrepo

import (
	"context"
	"os"
	"path/filepath"
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
	atomtest.DeclaresItsRow(t, Def, "orbit", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

func TestOrbitRepo(t *testing.T) {
	laid := "[[consumes]]\nfrom = \"b\"\ncontract = \"a-b\"\n"
	t.Run("no orbit.toml is absent", func(t *testing.T) {
		atomtest.Expect(t, run(atomtest.TreeIn(t, map[string]string{"a": "x"})), atomtest.StateOf(0), atomtest.Absent, "carries no root orbit.toml")
	})
	t.Run("foundry-dies main not supplied is a 2", func(t *testing.T) {
		in := atomtest.TreeIn(t, map[string]string{"orbit.toml": laid})
		in.Origin = "http://door/rob/a.git"
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "(-dies)")
	})
	t.Run("an orbit.toml that will not read is a 2", func(t *testing.T) {
		in := atomtest.TreeIn(t, map[string]string{"orbit.toml/x": "x"})
		in.Dies = atomtest.DiesAt(t, nil)
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "orbit:repo: CANNOT RUN")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		atomtest.Expect(t, run(atomtest.MissingRoot(t)), atomtest.StateOf(2), atomtest.Cannot, "orbit:repo: CANNOT RUN")
	})
	for name, tc := range map[string]struct {
		prepare func(t *testing.T, dies string)
		needle  string
	}{
		"contracts that will not read":      {func(t *testing.T, d string) { atomtest.BrokenLink(t, d, "orbits/z.toml") }, "foundry-dies: orbits/*.toml could not be read"},
		"a roster shard that will not read": {func(t *testing.T, d string) { atomtest.BrokenLink(t, d, "fleet/repos/z.json") }, "foundry-dies: fleet/repos/z.json could not be read"},
		"a directory of orbits with no contract": {func(t *testing.T, d string) {
			if err := os.Remove(filepath.Join(d, "orbits", "a-b.toml")); err != nil {
				t.Fatal(err)
			}
		}, "foundry-dies/orbits does not compose"},
	} {
		t.Run("foundry-dies with "+name+" is a 2", func(t *testing.T) {
			in := atomtest.TreeIn(t, map[string]string{"orbit.toml": laid})
			in.Dies = atomtest.DiesAt(t, nil)
			tc.prepare(t, in.Dies)
			atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, tc.needle)
		})
	}
	t.Run("a laid edge no contract composes to is recorded against this star", func(t *testing.T) {
		in := atomtest.TreeIn(t, map[string]string{"orbit.toml": laid})
		in.Origin = "http://door/rob/a.git"
		in.Dies = atomtest.DiesAt(t, nil)
		v := run(in)
		atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "orbit:repo: ")
		if len(v.Findings) == 0 {
			t.Errorf("the laid file declares an edge the contracts do not compose to, and the lane recorded nothing:\n%s", v.Reason)
		}
	})
	t.Run("a contract that does not parse is a finding, not the directory's 2", func(t *testing.T) {
		in := atomtest.TreeIn(t, map[string]string{"orbit.toml": laid})
		in.Dies = atomtest.DiesAt(t, map[string]string{"orbits/bad.toml": "= not toml"})
		v := run(in)
		atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass)
		if len(v.Findings) == 0 {
			t.Errorf("no finding for the unparseable contract:\n%s", v.Reason)
		}
	})
}

const (
	laneContract = "version = \"1\"\nstatus = \"approved\"\nverbs = [\"neighbors\"]\n"
	themisRoster = `{"name":"themis","verb_prefix":"themis"}`
	uraniaRoster = `{"name":"urania","verb_prefix":"urania"}`
)

// laneDies is foundry-dies at main: one contract and its two stars.
func laneDies(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		"orbits/urania-themis.toml": laneContract,
		"orbits/README.md":          "not a contract",
		"fleet/repos/urania.json":   uraniaRoster,
		"fleet/repos/themis.json":   themisRoster,
		"fleet/repos/broken.json":   "not json",
	}
	for k, v := range extra {
		files[k] = v
	}
	return atomtest.TreeIn(t, files).Root
}

// starIn is a tree holding a laid orbit.toml, handed foundry-dies and its origin.
func starIn(t *testing.T, laid string, diesExtra map[string]string) atoms.Input {
	in := atomtest.TreeIn(t, map[string]string{"orbit.toml": laid})
	in.Origin = "http://door/rob/themis.git"
	in.Dies = laneDies(t, diesExtra)
	return in
}

func findingsOf(v checks.Verdict) string {
	var out []string
	for _, f := range v.Findings {
		out = append(out, f.Verdict+" "+f.Cause+" "+f.Subject)
	}
	return strings.Join(out, "\n")
}

func TestOrbitRepoJudgesTheLaidFile(t *testing.T) {
	cs, err := orbitcompose.ContractsOf(map[string][]byte{"urania-themis.toml": []byte(laneContract)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	laid := string(orbitcompose.RenderReference(orbitcompose.Compose(cs)[0]))
	v := run(starIn(t, laid, nil))
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "orbit:repo: 1 holds")
	if findingsOf(v) != "holds edge consumes urania" {
		t.Errorf("findings %s", findingsOf(v))
	}
	stale := strings.Replace(laid, "sha256:", "sha256:0", 1)
	v = run(starIn(t, stale, nil))
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "1 drifted", "the contract moved and this file did not")
}

// ONE BAD CONTRACT IN foundry-dies IS A FINDING ON THAT FILE in a star's own
// lane, and the star is still judged against the contracts that read.
func TestOrbitRepoReportsAnUnreadableContractAndJudgesTheRest(t *testing.T) {
	cs, err := orbitcompose.ContractsOf(map[string][]byte{"urania-themis.toml": []byte(laneContract)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	laid := string(orbitcompose.RenderReference(orbitcompose.Compose(cs)[0]))
	v := run(starIn(t, laid, map[string]string{"orbits/broken-themis.toml": "version = 1"}))
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass)
	got := findingsOf(v)
	if !strings.Contains(got, "holds edge consumes urania") || !strings.Contains(got, "drifted contract-unparseable orbits/broken-themis.toml") {
		t.Errorf("findings %s", got)
	}
}

// A HYPHENATED STAR IS ONE PARTY, split against foundry-dies' own roster.
func TestOrbitRepoSplitsAHyphenatedStarAgainstTheDiesRoster(t *testing.T) {
	v := run(starIn(t, "# x\n", map[string]string{
		"orbits/blade-runner-themis.toml": laneContract,
		"fleet/repos/blade-runner.json":   `{"name":"blade-runner","verb_prefix":"blade"}`,
	}))
	if got := findingsOf(v); !strings.Contains(got, "consumes blade-runner") || strings.Contains(got, "contract-unparseable") {
		t.Errorf("findings %s", got)
	}
}

func TestOrbitRepoCannotRunWithoutTheContracts(t *testing.T) {
	in := starIn(t, "# x\n", nil)
	if err := os.Remove(filepath.Join(in.Dies, "orbits", "urania-themis.toml")); err != nil {
		t.Fatal(err)
	}
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "foundry-dies/orbits does not compose")
}
