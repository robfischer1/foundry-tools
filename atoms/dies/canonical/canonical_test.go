package diescanonical

import (
	"context"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
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
		Def.Toolchain != "none" || Def.Surface != "any:**" || len(Def.Needs) != 0 {
		t.Errorf("%+v", Def)
	}
}

func TestGradesEveryCatalogDocumentsForm(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   checks.State
		result  string
		needles []string
	}{
		{"a document that is its own canonical form passes", atomtest.DieTree(nil), checks.StatePass, atomtest.Pass,
			[]string{"1 catalog document(s) are each their own canonical form"}},
		{"keys out of order diverge", atomtest.DieTree(map[string]string{"fleet/repos/b.json": "{\"b\": 1, \"a\": 2}\n"}), checks.StateFindings, atomtest.Findings,
			[]string{"1 of 2 catalog document(s)", "fleet/repos/b.json: diverges from its canonical form"}},
		{"a document that is not JSON is a finding", atomtest.DieTree(map[string]string{"fleet/repos/b.json": "{"}), checks.StateFindings, atomtest.Findings,
			[]string{"fleet/repos/b.json: not one JSON document"}},
		{"no document at all grades nothing", map[string]string{"policy/.manifest": "x", "fleet/repos/a/other.json": "x"}, checks.StateCannotRun, atomtest.Cannot,
			[]string{"carries no catalog entry"}},
		{"a document that will not read", map[string]string{"policy/.manifest": "x", "fleet/repos/a.json/x": "x"}, checks.StateCannotRun, atomtest.Cannot,
			[]string{"fleet/repos/a.json/ could not be read"}},
		{"off the die's source it is absent", map[string]string{"main.go": "x"}, checks.StatePass, atomtest.Absent,
			[]string{"this tree is not the policy die's source"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			atomtest.Expect(t, check(context.Background(), atomtest.TreeIn(t, tc.files)), tc.state, tc.result, tc.needles...)
		})
	}
}
