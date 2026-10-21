// Package fleetlanesacheck is the atom fleet:lane-sa-check: the ServiceAccount each armed CI
// lane resolves to in flux's prime/daedalus-jobs.yaml is the one ci-lane-sa-guard admits for
// that lane. One file holds the atom and both runners execute it.
package fleetlanesacheck

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. The tree's own checker is run with the venv's python.
var Def = atomkit.Def{
	ID: "fleet:lane-sa-check", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "The repo's own tools/lane-sa-check (flux): every lane daedalus arms resolves, from prime/daedalus-jobs.yaml alone, to the ServiceAccount ci-lane-sa-guard admits for it, or it names the lane (a mismatch is a 403 on every Job of the lane and stalls CI: flux#5622).",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"pytools"},
	Check:     check,
}

const id = "fleet:lane-sa-check"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check runs flux's tools/lane-sa-check (stdlib only). ABSENT on any tree that does not
// track the tool, which is every repo but flux. The tool grades itself: 1 is a lane that
// resolves to an account the guard refuses, 2 is "could not look", could-not-run whatever
// its words were.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return atomlib.RunChecker(ctx, row, in, atomlib.Checker{Phase: "lane-sa-check", Tool: "lane-sa-check", Python: "python", Two: "\nlane-sa-check: could not read prime/daedalus-jobs.yaml or the guard policy — did not look"})
}
