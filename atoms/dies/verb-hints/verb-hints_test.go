package diesverbhints

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationIsTheRowItIs(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "python" || Def.Surface != "any:**" || strings.Join(Def.Needs, ",") != "tool:uv" {
		t.Errorf("%+v", Def)
	}
}

const traceback = "Traceback (most recent call last):\n  File \"tools/verb_hints.py\", line 1\nAttributeError: 'list' object has no attribute 'get'"

func TestRunsTheCheckModeAndMapsItsExit(t *testing.T) {
	files := atomtest.DieTree(map[string]string{checker: "# checker"})
	for _, venv := range []bool{false, true} {
		atomtest.SetVenv(t, venv)
		for _, c := range []struct {
			name    string
			out     string
			code    int
			state   checks.State
			result  string
			needles []string
		}{
			{"holds", "verb-hints: holds {}", 0, checks.StatePass, atomtest.Pass, []string{"holds"}},
			{"a defect is the checker's 1", "verb-hints: no row: x_y is served and has no read/mutation hint row", 1, checks.StateFindings, atomtest.Findings, []string{"no row: x_y"}},
			{"the checker's own could-not-run stays 2", "verb-hints: could not run: boom", 2, checks.StateCannotRun, atomtest.Cannot, []string{"could not run: boom"}},
			{"a crash exits 1 and is still a 2", traceback, 1, checks.StateCannotRun, atomtest.Cannot, []string{"crashed", "AttributeError"}},
			{"a crash behind a defect line is still a 2", "verb-hints: drift: x\n" + traceback, 1, checks.StateCannotRun, atomtest.Cannot, []string{"crashed"}},
			{"a python that would not start never ran", "gone", -1, checks.StateCannotRun, atomtest.Cannot, []string{"never ran"}},
		} {
			t.Run(c.name, func(t *testing.T) {
				in, f := atomtest.UVTree(t, files, func(atoms.Cmd) (string, int) { return c.out, c.code })
				atomtest.Expect(t, check(context.Background(), in), c.state, c.result, c.needles...)
				atomtest.WantUV(t, f.Last(), in.Root, "", checker, "--check")
			})
		}
	}
}

func TestNeverWritesTheTable(t *testing.T) {
	// the default of the script is to write schema/verb-hints.json: a gate that
	// forgot --check would rewrite the tree it grades and pass.
	atomtest.SetVenv(t, true)
	in, f := atomtest.UVTree(t, atomtest.DieTree(map[string]string{checker: "# checker"}), nil)
	check(context.Background(), in)
	if got := f.Last().Args; got[len(got)-1] != "--check" {
		t.Errorf("ran as %v", got)
	}
}

func TestAbsentOffTheDiesSourceAndMissingCheckerIsATwo(t *testing.T) {
	atomtest.SetVenv(t, true)
	in, f := atomtest.UVTree(t, map[string]string{checker: "x"}, nil)
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Absent, "not the policy die's source")
	atomtest.Assert(t, len(f.Calls) == 0, "an absent atom touched %v", f.Ran())

	in, f = atomtest.UVTree(t, atomtest.DieTree(nil), nil)
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, checker+" is absent, so there is no checker to run")
	atomtest.Assert(t, len(f.Calls) == 0, "uv ran: %v", f.Ran())
}
