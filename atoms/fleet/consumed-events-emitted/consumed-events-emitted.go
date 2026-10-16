// Package fleetconsumedeventsemitted is the atom fleet:consumed-events-emitted: every event_type the
// tree consumes has an emitter somewhere in the fleet. One file holds the atom, and both runners
// execute it (lld.md FN-085).
package fleetconsumedeventsemitted

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It runs in lead: the tree it reads is the pod's, and
// nothing else needs a toolchain.
var Def = atomkit.Def{
	ID: "fleet:consumed-events-emitted", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "Every event_type this repo consumes has at least one live emitter in the fleet, so a retired producer cannot leave a reader silent.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:consumed-events-emitted"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// consumedEventsEmitted: every event_type the tree consumes has an emitter
// somewhere in the fleet. The tree is read locally and the fleet only when it
// must be.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	var wanted []string
	for _, p := range in.Files {
		if checks.EventSourceExt(p) {
			wanted = append(wanted, p)
		}
	}
	files, bad := checks.ReadAll(in.Tree().Read, wanted)
	if bad != "" {
		return checks.VerdictOf(row, int(checks.StateCannotRun), "fleet:consumed-events-emitted: CANNOT RUN - "+bad)
	}
	state, report := checks.ConsumedEventsEmitted(ctx, files, func(ctx context.Context, need []string) (map[string]string, error) {
		return checks.FleetEmitters(ctx, in.DoorOf(), "", need)
	})
	return checks.VerdictOf(row, state, report)
}
