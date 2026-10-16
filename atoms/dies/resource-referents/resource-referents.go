// Package diesresourcereferents is the atom dies:resource-referents: every non-null face of a resource family in
// policy/resources/data.json names a served wire name, i.e. a verb with a
// schema/tool-output schema (F17 remainder W6).
package diesresourcereferents

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:resource-referents", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every non-null face in policy/resources/data.json names a served wire name that has a schema/tool-output schema; a null face is owed to the CRUD gate and not graded.",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:uv"},
	Check:     check,
}

const id = "dies:resource-referents"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check runs the tree's checker. After Wave 1 that directory is the live
// surface, so the check needs no dial. THE CHECKER IS THE TREE'S and the exit
// code is the verdict: 0 holds, 1 a dangling or malformed face, 2 could not run.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return dieskit.Checker(ctx, row, in, "", "tools/check_resource_referents.py")
}
