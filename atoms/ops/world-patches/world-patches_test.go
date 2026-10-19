package opsworldpatches

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
	atomtest.DeclaresItsRow(t, Def, "precommit", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

func TestTheChecker(t *testing.T) {
	atomtest.CheckerSuite(t, check, atomtest.Checker{ID: "ops:world-patches", Tool: "world-patches", Python: "python", Args: nil, Executable: false})
}

// Drift is a finding, and the tool's 2 is a could-not-run.
func TestWorldPatchesDriftIsAFindingAndTwoIsACouldNotRun(t *testing.T) {
	in, _ := atomtest.CheckerTree(t, "world-patches", 0o755, func(atoms.Cmd) (string, int) { return "World narcissus: writes out env", 1 })
	atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "World narcissus: writes out env", "world-patches failed (rc=1) — findings")
	in, _ = atomtest.CheckerTree(t, "world-patches", 0o755, func(atoms.Cmd) (string, int) { return "could not read prime/", 2 })
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "could not read prime/", "world-patches: could not read prime's kustomization or its Worlds — did not look")
}
