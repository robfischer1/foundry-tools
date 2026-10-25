// Package basesrelease is the atom bases:release: the per-base release of a
// repository of base images. A base that ends FROM scratch has no base image to
// build on, so it carries its own release inputs (a package.json and bun.lock
// whose "release" script leaves the layer's files under the base's own release/)
// and this atom runs them, one base after another, with the base's directory as
// the root, staging what each leaves for the Dockerfile that copies it from
// release/<base>/. Only lanes/build.toml names it (stage bases is in no
// lane's stages); the decisions are internal/releasekit's.
package basesrelease

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/releasekit"
)

const id = "bases:release"

// Def is the atom. It builds in the container the plan gives the bun toolchain,
// which on the executor runs the image the bases' Dockerfiles name in their
// release stage.
var Def = atomkit.Def{
	ID: "bases:release", Stage: checks.StageBases, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Each base that carries a package.json and copies its files from release/<base>/ runs its own release script on the builder image its Dockerfile names, and the files it leaves are staged there.",
	Surface:   "any:bases/*/package.json",
	Toolchain: "bun",
	Timeout:   45 * time.Minute,
	Check:     check,
}

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageBases, Lane: checks.LaneAny}

// check runs, for each base with a release root, the frozen install and the
// base's own `release` script in the base's directory (the steps ts:release runs
// at a star's root), and carries what the script left under the base's release/
// to <release out>/<base>/.
//
// A bun bundle embeds the absolute directory it was built in, so where the pod
// gives the container a tree of its own at /src (releasekit.SrcEnv) the steps
// run there with nothing copied; otherwise the tree is copied, less any release/
// and node_modules, so the checkout is left as it was found. The first failure
// stops the chain and names the base.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	tree := releasekit.DiskOf(in)
	roots, refused, err := releasekit.BaseRoots(ctx, tree)
	switch {
	case err != nil:
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the tree could not be read: "+err.Error())
	case len(refused) > 0:
		return checks.VerdictOf(row, int(checks.StateFindings), id+": FINDINGS - "+strings.Join(refused, "; "))
	case len(roots) == 0:
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - no base carries a package.json beside a Dockerfile that copies from "+buildlane.ReleaseDir+"/<base>/, so no base has a release of its own")
	}
	build, inPlace := releasekit.BuildRoot()
	if !inPlace {
		// a build directory that cannot be cleared fails the copy below
		_ = os.RemoveAll(build)
		if err := releasekit.CopyTree(in.Root, build, append(releasekit.TSExcludes, releasekit.BasesDir+"/*/"+buildlane.ReleaseDir)); err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the tree could not be copied: "+err.Error())
		}
	}
	var made []string
	var last checks.Verdict
	for _, root := range roots {
		dir := filepath.Join(build, filepath.FromSlash(root.Dir()))
		// a release/ left by an earlier run in a tree the container owns
		_ = os.RemoveAll(filepath.Join(dir, buildlane.ReleaseDir))
		last = releasekit.RunSteps(ctx, in, row, dir, nil, releasekit.TSSteps())
		if last.State != int(checks.StatePass) {
			last.Reason = fmt.Sprintf("%s: %s", root.Dir(), last.Reason)
			return last
		}
		files := releasekit.Entries(filepath.Join(dir, buildlane.ReleaseDir))
		if len(files) == 0 {
			return checks.VerdictOf(row, int(checks.StateFindings), id+": FINDINGS - "+root.Dir()+"'s release steps ran clean and left nothing under its "+buildlane.ReleaseDir+"/ — "+root.Dockerfile+" would have nothing to copy")
		}
		if err := releasekit.Carry(filepath.Join(dir, buildlane.ReleaseDir), filepath.Join(releasekit.OutDir(), root.Name)); err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+root.Dir()+"'s release could not be carried out: "+err.Error())
		}
		made = append(made, fmt.Sprintf("%s/%s/ holds %s", buildlane.ReleaseDir, root.Name, strings.Join(files, ", ")))
	}
	last.Reason = fmt.Sprintf("release build: `bun run release` in %d base(s); %s\n%s", len(roots), strings.Join(made, "; "), last.Reason)
	return last
}
