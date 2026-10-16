// Package lawbudget is the atom law:budget, one of the law lanes (Nomos F5b):
// no render's always-on context outgrows its ceiling.
// The judgement is internal/govlaw's; this hands it the tree. The lane is ABSENT
// on a tree that is not foundry-stocks, decided there from the path list.
package lawbudget

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/govlaw"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "law:budget", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "No render's always-on context document grows past its ceiling in budget.toml; raising a ceiling is a line in the same commit.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "law:budget"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check hands the tree to the lane.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	return atoms.RunLaw(row, in, govlaw.Budget)
}
