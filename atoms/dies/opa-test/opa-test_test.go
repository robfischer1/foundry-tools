package diesopatest

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

// TestUT_485_TheDeclarationIsTheRowItReplaced: the Def carries the identity the
// catalogue row did, and validates.
func TestUT_485_TheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "none" || Def.Surface != "any:**" || strings.Join(Def.Needs, ",") != "tool:opa" {
		t.Errorf("%+v", Def)
	}
}

func TestRunsTheSuiteAndFoldsOpasCodes(t *testing.T) {
	pinned := atomtest.PinnedOpa()
	test := func(out string, code int) map[string]func(atoms.Cmd) (string, int) {
		return map[string]func(atoms.Cmd) (string, int){"test": atomtest.Const0(out, code)}
	}
	for _, tc := range []struct {
		name    string
		script  atomtest.OpaScript
		ranTest bool
		state   checks.State
		result  string
		needles []string
	}{
		{"a suite with assertions passes", atomtest.OpaScript{Version: pinned, On: test("PASS: 12/12", 0)}, true, checks.StatePass, atomtest.Pass, []string{"12 assertion(s) pass"}},
		{"a zero-test run is refused", atomtest.OpaScript{Version: pinned, On: test("", 0)}, true, checks.StateCannotRun, atomtest.Cannot, []string{"REFUSING a zero-test run"}},
		{"a failing assertion (opa's 2) is a finding", atomtest.OpaScript{Version: pinned, On: test("FAIL: 1/3", 2)}, true, checks.StateFindings, atomtest.Findings,
			[]string{"FINDINGS - the rego suite did not come back clean", "FAIL: 1/3"}},
		{"a load error (opa's 1) is a finding", atomtest.OpaScript{Version: pinned, On: test("rego_parse_error", 1)}, true, checks.StateFindings, atomtest.Findings, []string{"rego_parse_error"}},
		{"a code opa does not use is a could-not-run", atomtest.OpaScript{Version: pinned, On: test("killed", 137)}, true, checks.StateCannotRun, atomtest.Cannot, []string{"opa exited 137"}},
		{"an opa that would not start never ran", atomtest.OpaScript{Version: pinned, On: test("opa: gone", -1)}, true, checks.StateCannotRun, atomtest.Cannot, []string{"the atom never ran: opa: gone"}},
		{"an opa that is not the pinned one", atomtest.OpaScript{Version: "Version: 0.1.0"}, false, checks.StateCannotRun, atomtest.Cannot, []string{"does not answer", "A policy suite that never ran"}},
		{"an opa that does not run", atomtest.OpaScript{Version: "opa: not found", VCode: -1}, false, checks.StateCannotRun, atomtest.Cannot, []string{"does not run (exit -1)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := atomtest.ToolTree(t, atomtest.DieTree(nil), tc.script.Answer)
			atomtest.Expect(t, check(context.Background(), in), tc.state, tc.result, tc.needles...)
			last := f.Last()
			if tc.ranTest != (atomtest.Flagged(last) == "test policy/ -v --timeout 120s") || last.Dir != in.Root {
				t.Errorf("the last call was %q in %q; the suite should have run: %v", atomtest.Flagged(last), last.Dir, tc.ranTest)
			}
		})
	}
	t.Run("the fleet derivation joins the suite where the tree carries it", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, atomtest.DieTree(map[string]string{checks.FleetDerive: "x"}),
			atomtest.OpaScript{Version: pinned, On: test("PASS: 3/3", 0)}.Answer)
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
		if got := atomtest.Flagged(f.Last()); !strings.Contains(got, checks.FleetDerive) {
			t.Errorf("opa was run as %q, lacking %s", got, checks.FleetDerive)
		}
	})
	t.Run("off the die's source it is absent and opa is not touched", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, map[string]string{"main.go": "x"}, nil)
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Absent, "this tree is not the policy die's source")
		if len(f.Calls) != 0 {
			t.Errorf("an absent atom touched %v", f.Ran())
		}
	})
}
