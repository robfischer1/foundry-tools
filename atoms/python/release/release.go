// Package pythonrelease is the atom python:release: the venv the image will
// carry builds from the star's lock on the image's own python base. The build
// is the build lane's too, so its logic is internal/releasekit's and both
// runners call it.
package pythonrelease

import (
	"context"
	"path/filepath"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/releasekit"
)

// Def is the atom. It names the star by its answers file or its record in
// foundry-dies, so it needs the dies. It builds the venv where the image will
// carry it (releasekit.AppEnv) and writes it to the release directory
// (releasekit.OutEnv) at app/.venv, for `COPY release/app /app`.
var Def = atomkit.Def{
	ID: "python:release", Stage: checks.StagePrepush, Lane: checks.LanePython,
	Group:         checks.GroupLanguage,
	Desc:          "The venv the image will carry builds from the star's lock on the image's own python base — for a Dockerfile on foundry/base-images/python that copies from release/; any other image is absent.",
	Surface:       "root:pyproject.toml",
	Toolchain:     "python",
	Needs:         []string{"dies", "tool:uv"},
	Timeout:       45 * time.Minute,
	NeedsManifest: true,
	Check:         check,
}

const id = "python:release"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LanePython}

// check syncs the star's lock into a venv at the image's /app.
//
// THE IMAGE'S BASE DECIDES WHICH RELEASE RUNS, as ts:release's does: a tree
// declaring a go.mod and the pyproject its Go port left behind (mnemosyne) is on
// the go base, and python:release is not its release. The runner puts the atom
// in the python base's container, so the interpreter, and the paths baked into
// the venv, are the ones the image runs (checks.PythonReleaseApp says what goes
// wrong otherwise).
//
// The tree is copied to the app directory without a local .venv or release/,
// then the sync that builds <app>/.venv from the star's lock
// (checks.PythonReleaseArgs). uv exits 2 on every error, so releasekit reads its
// failures by their words (checks.ReleaseStepState).
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	tree := releasekit.DiskOf(in)
	sub, stood := releasekit.Resolve(ctx, tree, row, in.Origin, releasekit.Builds)
	if stood != nil {
		return *stood
	}
	ref, stood := releasekit.OnBase(row, sub, "python")
	if stood != nil {
		return *stood
	}
	app := releasekit.AppDir()
	if err := releasekit.CopyTree(in.Root, app, releasekit.PythonExcludes); err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the tree could not be copied: "+err.Error())
	}
	v := releasekit.RunSteps(ctx, in, row, app, nil, [][]string{checks.PythonReleaseArgs()})
	if v.State != int(checks.StatePass) {
		return v
	}
	venv := filepath.Join(app, ".venv")
	made := releasekit.Entries(filepath.Join(venv, "bin"))
	if len(made) > 0 {
		if err := releasekit.Carry(venv, filepath.Join(releasekit.OutDir(), "app", ".venv")); err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the venv could not be carried out: "+err.Error())
		}
	}
	return releasekit.PythonMade(row, ref, made, v)
}
