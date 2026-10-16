// Package lawverbliveness is the atom law:verb-liveness, one of the law lanes (Nomos F5b):
// every verb the law names is served and none is retired.
// The judgement is internal/govlaw's; this hands it the tree. The lane is ABSENT
// on a tree that is not foundry-stocks, decided there from the path list.
package lawverbliveness

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/govlaw"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "law:verb-liveness", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every MCP verb a governance block or rendered context document names is on hades's wire surface and none is in retired-verbs.toml.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "law:verb-liveness"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check hands the tree to the lane.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	return atoms.RunLaw(row, in, govlaw.VerbLiveness)
}
