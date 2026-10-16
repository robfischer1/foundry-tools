// Package dieswitregenerated is the atom dies:wit-regenerated: the committed wit/ is what the generator
// makes of schema/ (stellar-core F2).
package dieswitregenerated

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:wit-regenerated", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The committed wit/ is byte-for-byte what wit-from-schema generates from schema/ — a schema change without regeneration, or a hand edit of a generated file, fails.",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:uv"},
	Check:     check,
}

const id = "dies:wit-regenerated"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check runs the tree's checker, which regenerates in memory and compares bytes:
// a schema edit nobody re-ran, a hand edit of a generated file and a generated
// file the generator stopped emitting are one failure. A refusal in the
// generator's report is NOT a finding here: the report is itself a generated,
// compared file. THE EXIT CODE IS THE VERDICT, unmapped: 0 identical, 1 stale,
// 2 the check could not run.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return dieskit.Checker(ctx, row, in, "", "tools/check_wit_regenerated.py")
}
