package diescapabilitiesserved

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

// The Def needs a literal id; the catalogue's other readers keep the constant.
func TestTheIdIsTheCataloguesConstant(t *testing.T) {
	if Def.ID != checks.CapabilitiesServedID || id != checks.CapabilitiesServedID {
		t.Errorf("%s, %s, want %s", Def.ID, id, checks.CapabilitiesServedID)
	}
}

func TestGradesDeclaredAgainstServed(t *testing.T) {
	decl := "{\"stars\":{\"athena\":{\"prefix\":\"plan\",\"verbs\":[\"task_read\"],\"tier\":{\"task_read\":\"default\"}}}}\n"
	served := "{\"schema\":1,\"star\":\"athena\",\"verbs\":[\"task_read\"]}\n"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   checks.State
		result  string
		needles []string
	}{
		{"declared exactly as served passes", atomtest.DieTree(map[string]string{
			"fleet/repos/athena.json": "{\"verb_prefix\":\"plan\"}\n", "policy/capabilities/data.json": decl, "capabilities/served/athena.json": served,
		}), checks.StatePass, atomtest.Pass, []string{"1 verb(s) across 1 star(s)"}},
		{"a die that has not adopted capabilities is absent", atomtest.DieTree(nil), checks.StatePass, atomtest.Absent, []string{"neither policy/capabilities/data.json"}},
		{"a served verb the document omits is findings", atomtest.DieTree(map[string]string{
			"fleet/repos/athena.json": "{\"verb_prefix\":\"plan\"}\n", "policy/capabilities/data.json": decl,
			"capabilities/served/athena.json": "{\"schema\":1,\"star\":\"athena\",\"verbs\":[\"task_read\",\"task_new\"]}\n",
		}), checks.StateFindings, atomtest.Findings, []string{"served but not declared (denied to every caller): task_new"}},
		{"a declared verb nothing serves is findings", atomtest.DieTree(map[string]string{
			"fleet/repos/athena.json": "{\"verb_prefix\":\"plan\"}\n", "policy/capabilities/data.json": decl,
			"capabilities/served/athena.json": "{\"schema\":1,\"star\":\"athena\",\"verbs\":[]}\n",
		}), checks.StateFindings, atomtest.Findings, []string{"declared but not served: task_read"}},
		{"a declaration landed beside the path the gate reads is findings, not absent", atomtest.DieTree(map[string]string{
			"fleet/repos/athena.json": "{\"verb_prefix\":\"plan\"}\n", "policy/capabilities/stars/data.json": decl,
		}), checks.StateFindings, atomtest.Findings, []string{"not policy/capabilities/data.json"}},
		{"a document with no stars key is findings even with no exports", atomtest.DieTree(map[string]string{
			"policy/capabilities/data.json": "{}\n",
		}), checks.StateFindings, atomtest.Findings, []string{"no \"stars\" object"}},
		{"declared with no export committed is findings", atomtest.DieTree(map[string]string{
			"fleet/repos/athena.json": "{\"verb_prefix\":\"plan\"}\n", "policy/capabilities/data.json": decl,
		}), checks.StateFindings, atomtest.Findings, []string{"athena: declared in policy/capabilities/data.json but no served-verbs export"}},
		{"off the die's source it is absent", map[string]string{"main.go": "x"}, checks.StatePass, atomtest.Absent, []string{"this tree is not the policy die's source"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			atomtest.Expect(t, check(context.Background(), atomtest.TreeIn(t, tc.files)), tc.state, tc.result, tc.needles...)
		})
	}
}
