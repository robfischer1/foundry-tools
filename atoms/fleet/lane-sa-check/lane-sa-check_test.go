package fleetlanesacheck

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
	atomtest.CheckerSuite(t, check, atomtest.Checker{ID: "fleet:lane-sa-check", Tool: "lane-sa-check", Python: "python", Args: nil, Executable: false})
}

// A lane on a refused account is a finding, and the tool's 2 is a could-not-run.
func TestLaneSAMismatchIsAFindingAndTwoIsACouldNotRun(t *testing.T) {
	in, _ := atomtest.CheckerTree(t, "lane-sa-check", 0o755, func(atoms.Cmd) (string, int) { return "bundle: resolves to ca-recipe", 1 })
	atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "bundle: resolves to ca-recipe", "lane-sa-check failed (rc=1) — findings")
	in, _ = atomtest.CheckerTree(t, "lane-sa-check", 0o755, func(atoms.Cmd) (string, int) { return "could not look: no such file", 2 })
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "could not look: no such file", "lane-sa-check: could not read prime/daedalus-jobs.yaml or the guard policy — did not look")
}
