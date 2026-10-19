package opsspecs

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
	atomtest.DeclaresItsRow(t, Def, "precommit", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

func TestTheChecker(t *testing.T) {
	atomtest.CheckerSuite(t, check, atomtest.Checker{ID: "ops:specs", Tool: "console-specs", Python: "python", Args: []string{"--check"}, Executable: false})
}

// ops:specs' tool compares the tree's copy to nas01-stacks, cloned live when
// nothing names a source; the atom names none, so the tool's own fallback is what
// runs. Its 2 is a could-not-run whatever its words were.
func TestSpecsLeavesTheSourceToTheTool(t *testing.T) {
	t.Run("an unreachable source is the tool's exit 2", func(t *testing.T) {
		in, f := atomtest.CheckerTree(t, "console-specs", 0o755, func(atoms.Cmd) (string, int) { return "could not clone http://ourea:8215/nas01-stacks.git", 2 })
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "could not clone", "specs: could not read the source — did not look")
		last := f.LastCall(t)
		if strings.Contains(atomtest.Flagged(last), "--source") || strings.Contains(strings.Join(last.Env, " "), "CONSOLE_SPECS_SOURCE") {
			t.Errorf("the atom named a source, which would pin the live clone: %+v", last)
		}
	})
	t.Run("drift is a finding", func(t *testing.T) {
		in, _ := atomtest.CheckerTree(t, "console-specs", 0o755, func(atoms.Cmd) (string, int) { return "drift: repo-console.readmodel.spec", 1 })
		atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "drift: repo-console.readmodel.spec", "specs failed (rc=1) — findings")
	})
}
