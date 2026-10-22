// Package fleetopengrepsast is the atom fleet:opengrep-sast: the SAST scan, which
// refuses a zero-file scan. The scan is internal/sastcheck's, which dev:go-sast
// runs too; both runners execute it (lld.md FN-085).
package fleetopengrepsast

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/sastcheck"
)

// Def is the atom. opengrep is a pinned tool the lane's image carries; the atom
// execs it by name.
var Def = atomkit.Def{
	ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "SAST scan that refuses a zero-file scan.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:opengrep"},
	Check:     check,
}

const id = "fleet:opengrep-sast"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check is the SAST scan, which refuses a zero-file scan.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return sastcheck.Check(ctx, in, row)
}
