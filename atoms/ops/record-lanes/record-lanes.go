// Package opsrecordlanes is the atom ops:record-lanes: flux's two record-lane
// lists are one set and hold every lane the executor runs. One file holds the
// atom and both runners execute it (lld.md FN-085).
package opsrecordlanes

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It reads two files of the flux tree, so it runs in lead.
var Def = atomkit.Def{
	ID: "ops:record-lanes", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Flux's RECORD_LANES (prime/daedalus-jobs.yaml) and record_lanes (the TOML in prime/ourea-config.yaml) are the same set, and each holds every EXECUTOR_LANES lane plus atoms and dev - a lane that owes a record to one reader and not the other settles differently depending on who asks.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "ops:record-lanes"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check: flux's two record-lane lists are one set and hold every lane
// the executor runs (checks.RecordLanes is the judgement; this reads the tree).
// A tree with neither file is not the flux tree (ABSENT); a tree with one of
// them is NOT absence - the other moved, and "agrees" would be unlooked-at.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	t := in.Tree()
	files := map[string]string{}
	for _, p := range in.Files {
		if !checks.IsRecordLanesFile(p) {
			continue
		}
		body, err := t.Read(p)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
				"%s: CANNOT RUN - %s would not read: %v", id, p, err))
		}
		files[p] = body
	}
	if len(files) == 0 {
		return atomlib.PhaseAbsent(row, "this tree carries neither "+checks.RecordLanesJobsFile+" nor "+checks.RecordLanesOureaFile+", so it is not the fleet's flux tree")
	}
	state, report := checks.RecordLanes(files)
	return checks.VerdictOf(row, state, report)
}
