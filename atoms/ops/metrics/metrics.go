// Package opsmetrics is the atom ops:metrics: every metric a rule or dashboard
// reads is on a keep-list. One file holds the atom and both runners execute it
// (lld.md FN-085).
package opsmetrics

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. The tree's own checker is run with the venv's python.
var Def = atomkit.Def{
	ID: "ops:metrics", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The repo's own tools/metric-allowlist (flux): every kube_*/container_* name a rule or dashboard reads is on a keep-list some job ingests, or it names which are not.",
	Surface:   "any:**",
	Toolchain: "python",
	Check:     check,
}

const id = "ops:metrics"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check: every metric a rule or dashboard reads is on a keep-list. Ingest is
// keep-lists, and a keep-list fails silently: a query against an omitted family
// reads "no data" forever. The tool grades itself: 2 is "could not read a
// keep-list or a source", could-not-run whatever its words were.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return atomlib.RunChecker(ctx, row, in, atomlib.Checker{Phase: "metrics", Tool: "metric-allowlist", Python: "python", Two: "\nmetrics: could not read a keep-list or a source — did not look"})
}
