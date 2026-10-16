package opsstarsa

import (
	"context"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "precommit", "python")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

func TestTheChecker(t *testing.T) {
	atomtest.CheckerSuite(t, check, atomtest.Checker{ID: "ops:star-sa", Tool: "star-sa-list", Python: "python", Args: nil, Executable: false})
}

// A stale list is a finding, and the tool's 2 is a could-not-run.
func TestStarSAStaleListIsAFindingAndTwoIsACouldNotRun(t *testing.T) {
	in, _ := atomtest.CheckerTree(t, "star-sa-list", 0o755, func(atoms.Cmd) (string, int) { return "stale: star-sa-admission.yaml", 1 })
	atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "stale: star-sa-admission.yaml", "star-sa failed (rc=1) — findings")
	in, _ = atomtest.CheckerTree(t, "star-sa-list", 0o755, func(atoms.Cmd) (string, int) { return "could not read identity/", 2 })
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "could not read identity/", "star-sa: could not read the ClusterSPIFFEID CRs or the policy — did not look")
}
