package opsmetrics

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
	atomtest.CheckerSuite(t, check, atomtest.Checker{ID: "ops:metrics", Tool: "metric-allowlist", Python: "python", Args: nil, Executable: false})
}

// The tool grades itself, and its 2 is a could-not-run whatever its words were.
func TestMetricsTwoIsACouldNotRunWhateverItsWordsWere(t *testing.T) {
	in, _ := atomtest.CheckerTree(t, "metric-allowlist", 0o755, func(atoms.Cmd) (string, int) { return "could not read a source", 2 })
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "could not read a source", "metrics: could not read a keep-list or a source — did not look")
}
