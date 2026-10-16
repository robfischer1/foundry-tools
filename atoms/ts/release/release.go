// Package tsrelease is the atom ts:release: the repo's own release script
// leaves the image's files under release/, run on the image's own bun base. The
// build is the build lane's too, so its logic is internal/releasekit's and both
// runners call it.
package tsrelease

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/releasekit"
)

// Def is the atom. It names the star by its answers file or its record in
// foundry-dies, so it needs the dies. It builds in a copy of the tree and
// writes its files to the release directory (releasekit.OutEnv).
var Def = atomkit.Def{
	ID: "ts:release", Stage: checks.StagePrepush, Lane: checks.LaneTS,
	Group:     checks.GroupLanguage,
	Desc:      "The record's release steps leave the image's files under release/, run on the image's own bun base — for a Dockerfile on foundry/base-images/bun that copies from release/; any other image is absent.",
	Surface:   "root:package.json",
	Toolchain: "bun",
	Needs:     []string{"dies"},
	Timeout:   45 * time.Minute,
	Check:     check,
}

const id = "ts:release"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneTS}

// check runs the repo's own `release` script on the image's own bun base.
//
// THE IMAGE'S BASE DECIDES WHICH RELEASE RUNS. A tree can declare two
// toolchains, and planning runs every lane the tree declares, so this atom
// stands down unless the Dockerfile that asks for release/ is on the bun base
// (releasekit.OnBase). The runner puts the atom in that base's container; the
// atom reads the base from the Dockerfile to say which it was.
//
// A bun star's artifact is its bundle (calliope bundles one server.js, demeter
// builds a SPA beside it), so there is no convention for the steps: the root
// package.json defines a "release" script, the same interface as its gate,
// build and test scripts, and the fleet runs it after the frozen install.
//
// THE TREE IS COPIED, not built in place: what the steps write has to be read
// back, and the copy leaves out a release/ a local `just image` left behind and
// the node_modules the install makes. The first failure stops the chain, so its
// verdict names the step that failed.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	tree := releasekit.DiskOf(in)
	sub, stood := releasekit.Resolve(ctx, tree, row, in.Origin, releasekit.Builds)
	if stood != nil {
		return *stood
	}
	ref, stood := releasekit.OnBase(row, sub, "bun")
	if stood != nil {
		return *stood
	}
	build := releasekit.BuildDir()
	if err := os.RemoveAll(build); err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the build directory could not be cleared: "+err.Error())
	}
	if err := releasekit.CopyTree(in.Root, build, releasekit.TSExcludes); err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the tree could not be copied: "+err.Error())
	}
	v := releasekit.RunSteps(ctx, in, row, build, nil, releasekit.TSSteps())
	if v.State != int(checks.StatePass) {
		return v
	}
	made := releasekit.Entries(filepath.Join(build, buildlane.ReleaseDir))
	if len(made) > 0 {
		if err := releasekit.Carry(filepath.Join(build, buildlane.ReleaseDir), releasekit.OutDir()); err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the release could not be carried out: "+err.Error())
		}
	}
	return releasekit.TSMade(row, ref, made, v)
}
