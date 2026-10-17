package opsdeclaration

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
	atomtest.CheckerSuite(t, check, atomtest.Checker{ID: "ops:declaration", Tool: "declaration-integrity", Python: "python", Args: nil, Executable: false})
}

// The checker is run through python, so it need not be executable.
func TestDeclarationNeedNotBeTrackedExecutable(t *testing.T) {
	in, _ := atomtest.CheckerTree(t, "declaration-integrity", 0o644, nil)
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass)
}

// ops:declaration has no exit-2 case: any other exit is a finding, as the chain
// settled it.
func TestDeclarationHasNoExitTwoCase(t *testing.T) {
	in, _ := atomtest.CheckerTree(t, "declaration-integrity", 0o755, func(atoms.Cmd) (string, int) { return "a payload is undeclared", 2 })
	atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "a payload is undeclared", "declaration failed (rc=2) — findings")
}
