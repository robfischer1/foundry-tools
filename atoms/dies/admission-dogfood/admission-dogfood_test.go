package diesadmissiondogfood

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
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "none" || Def.Surface != "any:**" || strings.Join(Def.Needs, ",") != "tool:opa" {
		t.Errorf("%+v", Def)
	}
}

const denyOne = `{"result":[{"expressions":[{"value":["no stellar principal"]}]}]}`
const denyNone = `{"result":[{"expressions":[{"value":[]}]}]}`

func TestAsksTheDomainAboutOurOwnStar(t *testing.T) {
	pinned := atomtest.PinnedOpa()
	full := atomtest.DieTree(map[string]string{"policy/admission/a.rego": "x", "tests/fixtures/ouranos-self.json": "{}"})
	for _, tc := range []struct {
		name    string
		files   map[string]string
		eval    func(atoms.Cmd) (string, int)
		state   checks.State
		result  string
		needles []string
	}{
		{"a star the domain admits passes", full, atomtest.Const0(denyNone, 0), checks.StatePass, atomtest.Pass, []string{"deny count = 0; the admission domain admits our own star shape"}},
		{"a deny is a finding with its reasons", full, atomtest.Const0(denyOne, 0), checks.StateFindings, atomtest.Findings, []string{"deny count = 1", "  no stellar principal"}},
		{"an eval that did not complete is a 2", full, atomtest.Const0("rego_type_error", 3), checks.StateCannotRun, atomtest.Cannot, []string{"opa eval did not complete", "rego_type_error"}},
		{"an answer this atom cannot read is a 2", full, atomtest.Const0("{}", 0), checks.StateCannotRun, atomtest.Cannot, []string{"a shape this atom cannot read"}},
		{"an opa that would not start never ran", full, atomtest.Const0("opa: gone", -1), checks.StateCannotRun, atomtest.Cannot, []string{"the atom never ran: opa: gone"}},
		{"no admission domain is a 2", atomtest.DieTree(map[string]string{"policy/other/a.rego": "x", "tests/fixtures/ouranos-self.json": "{}"}), nil, checks.StateCannotRun, atomtest.Cannot, []string{"policy/admission is absent"}},
		{"a missing fixture is a 2", atomtest.DieTree(map[string]string{"policy/admission/a.rego": "x"}), nil, checks.StateCannotRun, atomtest.Cannot, []string{"tests/fixtures/ouranos-self.json is absent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := atomtest.OpaScript{Version: pinned}
			if tc.eval != nil {
				script.On = map[string]func(atoms.Cmd) (string, int){"eval": tc.eval}
			}
			in, f := atomtest.ToolTree(t, tc.files, script.Answer)
			atomtest.Expect(t, check(context.Background(), in), tc.state, tc.result, tc.needles...)
			if tc.eval == nil {
				if len(f.Calls) != 0 {
					t.Errorf("the atom settled before asking opa anything, and ran %v", f.Ran())
				}
				return
			}
			want := "eval -d policy/admission -i tests/fixtures/ouranos-self.json data.admission.deny --format json"
			if c := f.Calls[0]; atomtest.Flagged(c) != "version" {
				t.Errorf("opa was asked %q before its version was proved", atomtest.Flagged(c))
			}
			if c := f.Last(); atomtest.Flagged(c) != want || c.Dir != in.Root {
				t.Errorf("opa was run as %q in %q, want %q", atomtest.Flagged(c), c.Dir, want)
			}
		})
	}
	t.Run("an opa that is not the pinned one is a 2 and nothing is evaluated", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, full, atomtest.OpaScript{Version: "Version: 0.1.0"}.Answer)
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "does not answer", "A policy suite that never ran")
		if len(f.Calls) != 1 {
			t.Errorf("opa evaluated after failing its version: %v", f.Ran())
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
