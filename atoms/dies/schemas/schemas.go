// Package diesschemas is the atom dies:schemas: EVERY schema in the tree is checked,
// discovered rather than listed. dies:schema and dies:findings name their files
// and are silent about every other schema, so a schema nobody listed was never
// checked at all; a discovery step cannot forget.
package diesschemas

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:schemas", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "EVERY schema in schema/ is a valid Draft 2020-12 document, and every one with fixtures still refuses what it exists to refuse — discovered, never listed.",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:uv"},
	Check:     check,
}

const id = "dies:schemas"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check runs the tree's checker under uv with jsonschema. THE SCRIPT'S EXIT
// LADDER IS ALREADY THIS ATOM'S VERDICT: check_schemas.py answers the worst code
// across every schema (0 clean, 1 a fixture disagreed, 2 a schema is missing,
// unparseable or not valid draft-2020-12), and could-not-run outranks a finding.
// It requires no particular schema, so foundry-dies' layout is not pinned into
// this module.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return dieskit.Checker(ctx, row, in, dieskit.WithJSONSchema, "tools/check_schemas.py")
}
