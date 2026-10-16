// Package fleetrenovatelockstep is the atom fleet:renovate-lockstep: the Renovate image this repo
// pins is held to the one foundry/flux deploys. One file holds the atom, and both runners execute it
// (lld.md FN-085).
package fleetrenovatelockstep

import (
	"context"
	"fmt"
	"slices"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It runs in lead: the tree it reads is the pod's, and
// nothing else needs a toolchain.
var Def = atomkit.Def{
	ID: "fleet:renovate-lockstep", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "The Renovate image this repo pins (ImageRenovate) equals the one foundry/flux deploys, and flux's two containers agree.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:renovate-lockstep"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// renovateLockstep: the Renovate image this repo pins is held to the one
// foundry/flux deploys. The files are a literal list, so a tree that holds
// neither reads nothing.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	files := map[string]string{}
	for _, p := range checks.RenovateLockstepFiles {
		if !slices.Contains(in.Files, p) {
			continue
		}
		body, err := in.Tree().Read(p)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("fleet:renovate-lockstep: CANNOT RUN - %s would not read: %v", p, err))
		}
		files[p] = body
	}
	state, report := checks.RenovateLockstep(ctx, files, in.DoorOf())
	return checks.VerdictOf(row, state, report)
}
