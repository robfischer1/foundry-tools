package diestoolschemasserved

import (
	"context"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationCarriesTheIdentity(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "none" || Def.Surface != "any:**" || len(Def.Needs) != 0 {
		t.Errorf("%+v", Def)
	}
}

func TestTheIdIsTheCataloguesConstant(t *testing.T) {
	if Def.ID != checks.ToolSchemasServedID || id != checks.ToolSchemasServedID {
		t.Errorf("%s, %s, want %s", Def.ID, id, checks.ToolSchemasServedID)
	}
}

func TestGradesServedAgainstTheFiles(t *testing.T) {
	served := "{\"schema\":1,\"star\":\"a\",\"verbs\":[\"x\"]}\n"
	inputs := "{\"schema\":1,\"star\":\"a\",\"verbs\":{\"x\":{\"type\":\"object\"}}}\n"
	file := "{\"$id\":\"i\",\"type\":\"object\"}\n"
	clean := map[string]string{
		"fleet/repos/a.json": "{\"verb_prefix\":\"pa\"}\n", "capabilities/served/a.json": served, "capabilities/served-inputs/a.json": inputs,
		"schema/tool-input/pa_x.schema.json": file, "schema/tool-output/pa_x.schema.json": "{}\n",
	}
	without := func(k string) map[string]string {
		m := map[string]string{}
		for p, v := range clean {
			if p != k {
				m[p] = v
			}
		}
		return m
	}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   checks.State
		result  string
		needles []string
	}{
		{"served exactly as filed passes", atomtest.DieTree(clean), checks.StatePass, atomtest.Pass, []string{"1 star(s)"}},
		{"a die with no tool schemas is absent", atomtest.DieTree(nil), checks.StatePass, atomtest.Absent, []string{"ABSENT"}},
		{"a missing input file is findings", atomtest.DieTree(without("schema/tool-input/pa_x.schema.json")), checks.StateFindings, atomtest.Findings, []string{"a: pa_x has no schema/tool-input/pa_x.schema.json"}},
		{"a missing output file is findings", atomtest.DieTree(without("schema/tool-output/pa_x.schema.json")), checks.StateFindings, atomtest.Findings, []string{"a: pa_x has no schema/tool-output/pa_x.schema.json"}},
		{"off the die's source it is absent", map[string]string{"main.go": "x"}, checks.StatePass, atomtest.Absent, []string{"this tree is not the policy die's source"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			atomtest.Expect(t, check(context.Background(), atomtest.TreeIn(t, tc.files)), tc.state, tc.result, tc.needles...)
		})
	}
}
