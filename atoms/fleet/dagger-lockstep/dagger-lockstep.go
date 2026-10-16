// Package fleetdaggerlockstep is the atom fleet:dagger-lockstep: the dagger CLI, the engine and
// any dagger module in the tree are held to one version. One file holds the atom, and both runners
// execute it (lld.md FN-085).
package fleetdaggerlockstep

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
	ID: "fleet:dagger-lockstep", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "The dagger CLI, engine and module pins in this tree agree with the engine foundry/flux runs — CLI and engine move in lockstep.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:dagger-lockstep"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// daggerLockstep: the dagger CLI, the engine and any dagger module in the tree
// are held to one version. The pin files are a literal list, so a tree that
// holds none reads nothing.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	paths := []string{checks.DaggerModuleManifest}
	for _, f := range checks.DaggerPinFiles {
		paths = append(paths, f.Path)
	}
	files := map[string]string{}
	for _, p := range paths {
		if !slices.Contains(in.Files, p) {
			continue
		}
		body, err := in.Tree().Read(p)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("fleet:dagger-lockstep: CANNOT RUN - %s would not read: %v", p, err))
		}
		files[p] = body
	}
	state, report := checks.DaggerLockstep(ctx, files, in.DoorOf())
	return checks.VerdictOf(row, state, report)
}
