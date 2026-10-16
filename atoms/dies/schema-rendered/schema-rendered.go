// Package diesschemarendered is the atom dies:schema-rendered: every die schema is the render of urania's
// shapes (stellar-core F14).
package diesschemarendered

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:schema-rendered", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every die schema is the byte-for-byte render of the urania shapes its snapshot holds — a hand-edited schema, a schema with no shape, or a snapshot with no schema fails.",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:uv"},
	Check:     check,
}

const id = "dies:schema-rendered"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check runs the tree's checker. The lane holds this tree and nothing else, so
// it cannot ask the graph: the committed schema/<name>.shapes.json stands for the
// shapes, and the checker re-renders it and compares BYTES to the schema (a
// hand-edited schema, a schema with no snapshot, a snapshot with no schema, a
// hand-edited snapshot and a shape with no receipt are one failure each). THE
// EXIT CODE IS THE VERDICT, unmapped: 0 every schema is its render, 1 one is
// not, 2 the check could not run.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return dieskit.Checker(ctx, row, in, "", "tools/check_schema_rendered.py")
}
