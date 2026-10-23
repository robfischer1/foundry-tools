// Package fleetbgparity is the atom fleet:bg-parity: flux's two daedalus ConfigMaps (the
// blue and green colours) agree on the keys a flip turns into an outage. One file holds the
// atom and both runners execute it.
package fleetbgparity

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It reads two files of the flux tree, so it runs in lead.
var Def = atomkit.Def{
	ID: "fleet:bg-parity", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "Flux's prime/daedalus-jobs.yaml and prime/daedalus-jobs-green.yaml (daedalus' blue and green colours) agree on the consumer group, the Job namespace, RECORD_LANES, every lane's ServiceAccount and the CI_IDENTITY_V*_LANES lists - a difference there replays the ask topic or 403s every Job of a lane when the colours swap.",
	Surface:   "any:prime/daedalus-jobs*.yaml",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:bg-parity"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check compares the two colours' ConfigMaps (checks.BGParity is the judgement; this
// reads the tree). A tree with no green ConfigMap is from before the second colour (ABSENT).
// A green one with no blue one is NOT absence: the manifests moved.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	t := in.Tree()
	files := map[string]string{}
	for _, p := range in.Files {
		if !checks.IsBGParityFile(p) {
			continue
		}
		body, err := t.Read(p)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
				"%s: CANNOT RUN - %s would not read: %v", id, p, err))
		}
		files[p] = body
	}
	if _, green := files[checks.BGParityGreenFile]; !green {
		return atomlib.PhaseAbsent(row, "this tree carries no "+checks.BGParityGreenFile+", so daedalus has one colour and there is nothing to compare")
	}
	state, report := checks.BGParity(files)
	return checks.VerdictOf(row, state, report)
}
