package atomlib

import (
	"context"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

var checkerRow = checks.AtomDef{ID: "ops:x", Stage: checks.StagePrecommit, Lane: checks.LaneAny}

func TestRunCheckerHoldsTheShapeAllSixShare(t *testing.T) {
	run := func(ctx context.Context, in atoms.Input) checks.Verdict {
		return RunChecker(ctx, checkerRow, in, Checker{Phase: "x", Tool: "chk", Python: "python3", Args: []string{"--a"}})
	}
	atomtest.CheckerSuite(t, run, atomtest.Checker{ID: "ops:x", Tool: "chk", Python: "python3", Args: []string{"--a"}})
}

func TestRunCheckerLetsATwoSpeakInItsOwnWords(t *testing.T) {
	in, _ := atomtest.CheckerTree(t, "chk", 0o755, func(atoms.Cmd) (string, int) { return "could not read", 2 })
	v := RunChecker(context.Background(), checkerRow, in, Checker{Phase: "x", Tool: "chk", Python: "python3", Two: "\nx: did not look"})
	atomtest.Expect(t, v, atomtest.StateOf(2), atomtest.Cannot, "could not read", "x: did not look")
}

func TestAnExecutableCheckerMustBeTrackedExecutable(t *testing.T) {
	in, _ := atomtest.CheckerTree(t, "chk", 0o644, nil)
	if HasChecker(in, "chk", true) || !HasChecker(in, "chk", false) {
		t.Error("a checker run as a program has to be executable; one run through python need not be")
	}
}
