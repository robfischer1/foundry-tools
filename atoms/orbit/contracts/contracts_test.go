package orbitcontracts

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "orbit", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

func TestOrbitContracts(t *testing.T) {
	own := map[string]string{"orbits/bad.toml": "= not toml", "fleet/repos/a.json": atomtest.StarData("a")}
	t.Run("a tree that is not the contracts' repository is absent", func(t *testing.T) {
		atomtest.Expect(t, run(atomtest.TreeIn(t, map[string]string{"a": "x"})), atomtest.StateOf(0), atomtest.Absent, "not the contracts' repository")
	})
	t.Run("contracts without a roster beside them are absent too", func(t *testing.T) {
		atomtest.Expect(t, run(atomtest.TreeIn(t, map[string]string{"orbits/a-b.toml": "x"})), atomtest.StateOf(0), atomtest.Absent, "not the contracts' repository")
	})
	t.Run("a directory of notes is not a contract", func(t *testing.T) {
		notes := map[string]string{"orbits/README.md": "x", "fleet/repos/a.json": atomtest.StarData("a")}
		atomtest.Expect(t, run(atomtest.TreeIn(t, notes)), atomtest.StateOf(0), atomtest.Absent, "not the contracts' repository")
	})
	t.Run("a contract of this tree that will not read is a 2", func(t *testing.T) {
		in := atomtest.TreeIn(t, own)
		atomtest.BrokenLink(t, in.Root, "orbits/z.toml")
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "orbits/* could not be read")
	})
	t.Run("a roster shard that will not read is a 2", func(t *testing.T) {
		in := atomtest.TreeIn(t, own)
		atomtest.BrokenLink(t, in.Root, "fleet/repos/z.json")
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "fleet/repos/z.json could not be read")
	})
	t.Run("a contract that does not parse is recorded, report-only, and the atom stays 0", func(t *testing.T) {
		v := run(atomtest.TreeIn(t, own))
		atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "orbit:contracts: ")
		if len(v.Findings) != 1 || v.Findings[0].Verdict != checks.VerdictDrifted || v.Findings[0].Probe != id {
			t.Errorf("findings %+v: want one drifted finding for the bad contract (report-only until Enforce flips)", v.Findings)
		}
	})
}

const (
	laneContract = "version = \"1\"\nstatus = \"approved\"\nverbs = [\"neighbors\"]\n"
	themisRoster = `{"name":"themis","verb_prefix":"themis"}`
	uraniaRoster = `{"name":"urania","verb_prefix":"urania"}`
)

// contractsTree is foundry-dies itself under test.
func contractsTree(extra map[string]string) map[string]string {
	tree := map[string]string{
		"orbits/urania-themis.toml": laneContract,
		"orbits/README.md":          "the contracts",
		"fleet/repos/urania.json":   uraniaRoster,
		"fleet/repos/themis.json":   themisRoster,
	}
	for k, v := range extra {
		tree[k] = v
	}
	return tree
}

func findingsOf(v checks.Verdict) string {
	var out []string
	for _, f := range v.Findings {
		out = append(out, f.Verdict+" "+f.Cause+" "+f.Subject)
	}
	return strings.Join(out, "\n")
}

// The contracts' own tree holds: one approved contract between two stars on the
// roster, recorded as a holding finding.
func TestOrbitContractsHoldsOnTheContractsTree(t *testing.T) {
	v := run(atomtest.TreeIn(t, contractsTree(nil)))
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "orbit:contracts: 1 holds")
	if findingsOf(v) != "holds contract orbits/urania-themis.toml" || v.Findings[0].Probe != "orbit:contracts" {
		t.Errorf("findings %s", findingsOf(v))
	}
}

func TestOrbitContractsReportsABadContractWithoutFailing(t *testing.T) {
	v := run(atomtest.TreeIn(t, contractsTree(map[string]string{"orbits/urania-ghost.toml": laneContract})))
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "1 drifted", "[report-only: would be violated] ghost is not on the fleet roster")
	if !strings.Contains(findingsOf(v), "drifted party-not-on-roster orbits/urania-ghost.toml") {
		t.Errorf("findings %s", findingsOf(v))
	}
}

// A hyphenated contract on the contracts tree reads against its own roster.
func TestOrbitContractsHoldsAHyphenatedStarOnTheRoster(t *testing.T) {
	v := run(atomtest.TreeIn(t, contractsTree(map[string]string{
		"orbits/blade-runner-themis.toml": laneContract,
		"fleet/repos/blade-runner.json":   `{"name":"blade-runner","verb_prefix":"blade"}`,
	})))
	if got := findingsOf(v); !strings.Contains(got, "holds contract orbits/blade-runner-themis.toml") {
		t.Errorf("findings %s", got)
	}
}

func TestOrbitContractsIsAbsentOutsideTheContractsTree(t *testing.T) {
	for name, tree := range map[string]map[string]string{
		"a star":         {"go.mod": "module x"},
		"orbits alone":   {"orbits/urania-themis.toml": laneContract},
		"a roster alone": {"fleet/repos/urania.json": uraniaRoster},
	} {
		v := run(atomtest.TreeIn(t, tree))
		atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Absent, "orbit:contracts: ABSENT - this tree is not the contracts' repository")
		if v.Result != "absent" {
			t.Errorf("%s: result %q", name, v.Result)
		}
	}
}

// The orbit atoms are the orbit stage's only: none is in a pull's vector.
func TestOrbitContractsIsTheOrbitStageOnly(t *testing.T) {
	if Def.Stage != checks.StageOrbit {
		t.Errorf("stage %q", Def.Stage)
	}
	for _, p := range checks.PullPathAtoms() {
		if p.ID == id {
			t.Errorf("%s is in the gate's vector", id)
		}
	}
}
