// Package diesfindings is the atom dies:findings: the findings schema still says what it is
// for. THE CHECKER IS THE TREE'S: foundry-dies owns the schema and
// tools/check_findings.py, so the repo that owns a contract is the repo that goes
// red when it breaks.
package diesfindings

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:findings", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The findings schema is a valid Draft 2020-12 document and still refuses what it exists to refuse — every fixture classifies as its name claims.",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:uv"},
	Check:     check,
}

const id = "dies:findings"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check runs the tree's checker under uv with jsonschema, and its exit is the
// verdict: 0 for every fixture classified as its name claims, 1 when one is not,
// 2 when the run could not be made at all (a missing schema, an unparseable
// fixture). That is the three-state vocabulary the findings schema makes
// normative, so the mapping is an identity and not a switch.
//
// THE FIXTURES ARE NOT REQUIRED HERE, deliberately. An absent tests/findings is
// a could-not-run the script already reports, and a fixture set with no invalid-*
// case is a finding it already reports. Naming a fixture file in this atom would
// pin foundry-dies' test layout into foundry-tools for no gain.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	if stop := dieskit.Shape(t, row); stop != nil {
		return *stop
	}
	if stop := atoms.RequirePaths(t, row, [][2]string{
		{"schema/findings.schema.json", "schema/findings.schema.json is absent, so there is no schema to validate."},
		{"tools/check_findings.py", "tools/check_findings.py is absent, so there is no checker to run."},
	}); stop != nil {
		return *stop
	}
	return dieskit.Verdict(ctx, row, in, dieskit.WithJSONSchema, "tools/check_findings.py")
}
