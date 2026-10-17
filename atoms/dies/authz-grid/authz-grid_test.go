package diesauthzgrid

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

// The Def carries the identity the catalogue row will, and validates.
func TestTheDeclarationValidates(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "python" || Def.Surface != "any:**" || strings.Join(Def.Needs, ",") != "tool:opa,tool:uv" {
		t.Errorf("%+v", Def)
	}
}

func files(without ...string) map[string]string {
	f := atomtest.DieTree(map[string]string{checker: "# checker", golden: "{}", cells: "x"})
	for _, w := range without {
		delete(f, w)
	}
	return f
}

// script answers `opa version` as the pin and every other call (the python
// probe aside, which UVTree answers) with the checker's exit.
func script(version string, code int) func(atoms.Cmd) (string, int) {
	return func(c atoms.Cmd) (string, int) {
		if c.Name == "opa" {
			return version, 0
		}
		return "grid said so", code
	}
}

// The checker's exit IS the verdict, unmapped, and it is handed the list length;
// run under uv on a toolchain image and in the venv in the tools container.
func TestRunsTheCheckerAndPassesItsExitThrough(t *testing.T) {
	for _, venv := range []bool{false, true} {
		t.Run(map[bool]string{false: "under uv", true: "in the venv"}[venv], func(t *testing.T) {
			atomtest.SetVenv(t, venv)
			for _, c := range []struct {
				name   string
				code   int
				state  checks.State
				result string
				words  string
			}{
				{"every cell answers as recorded", 0, checks.StatePass, atomtest.Pass, "grid said so"},
				{"a moved answer is the checker's exit 1", 1, checks.StateFindings, atomtest.Findings, "grid said so"},
				{"a grid that could not be asked is its exit 2", 2, checks.StateCannotRun, atomtest.Cannot, "grid said so"},
				{"a python that would not start never ran", -1, checks.StateCannotRun, atomtest.Cannot, "the atom never ran: grid said so"},
			} {
				t.Run(c.name, func(t *testing.T) {
					in, f := atomtest.UVTree(t, files(), script(atomtest.PinnedOpa(), c.code))
					atomtest.Expect(t, check(context.Background(), in), c.state, c.result, c.words)
					atomtest.WantUV(t, f.Last(), in.Root, "", checker, "--first", listed)
				})
			}
		})
	}
}

// Nothing runs on a tree that cannot be graded: no checker, no golden, no cell
// list, an opa that is not the pin, or off the die's source.
func TestRefusesBeforeRunningAnythingItCannotGrade(t *testing.T) {
	for _, c := range []struct {
		name    string
		tree    map[string]string
		version string
		state   checks.State
		result  string
		needle  string
		calls   []string
	}{
		{"no checker", files(checker), atomtest.PinnedOpa(), checks.StateCannotRun, atomtest.Cannot, checker + " is absent", nil},
		{"no recorded digest", files(golden), atomtest.PinnedOpa(), checks.StateCannotRun, atomtest.Cannot, "no recorded digest to compare with", nil},
		{"no recorded cell list", files(cells), atomtest.PinnedOpa(), checks.StateCannotRun, atomtest.Cannot, "no recorded cell list", nil},
		{"an opa that is not the pin", files(), "Version: 0.1.0", checks.StateCannotRun, atomtest.Cannot, "does not answer", []string{"opa"}},
		{"not the die's source", map[string]string{checker: "x"}, atomtest.PinnedOpa(), checks.StatePass, atomtest.Absent, "not the policy die's source", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			in, f := atomtest.UVTree(t, c.tree, script(c.version, 0))
			atomtest.Expect(t, check(context.Background(), in), c.state, c.result, c.needle)
			if got := f.Ran(); strings.Join(got, "|") != strings.Join(c.calls, "|") {
				t.Errorf("ran %v, want %v", got, c.calls)
			}
		})
	}
}
