package main

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/releasekit"
)

// THE RELEASE BUILDS THE BUILD AND CAST LANES CONSUME (CA master-plan F14/F17).
// The go, python, rust and ts release atoms are files now
// (atoms/<ns>/release), run by the adapter and by the executor; what each DECIDES
// — whose image this is, what the build produces, which argv, what the exit
// means — is internal/releasekit's, read through runTree here and through the
// checkout in a pod. What stays in this file is the engine's half of the build
// lanes' Release(): the compile as a WithExec chain whose Directory the image is
// built from, until step 6's executor capability takes that over.
//
// Release() composes the same pieces the atoms do — releasekit.StarName,
// ReleaseDockerfile, GoPlan, RustPlan, GoRoot, the argvs in checks/release.go,
// the exclusions and the step verdicts — so the gate's verdict and the build
// lane's artifact cannot disagree about whose compile it is. It does not share
// the gate's engine exec any more: the atom runs its compile inside one atom
// exec, where Release() runs its own chain.
//
// A bun star's artifact is its bundle and a python star's its venv. Both are
// built ON THE IMAGE'S OWN BASE — the base the Dockerfile's runtime stage FROMs —
// so the runtime the artifact was made with is the one that runs it
// (checks.PythonReleaseApp says what goes wrong otherwise). The build lane stages
// the result at release/ exactly as it stages a binary (build.go stageRelease),
// and the Dockerfile is a FROM and a COPY.

// onBase is the lane container for a release: the image's own base, with the
// lane's environment (laneBase: CI, the cache CA's readers, telemetry off)
// and the lane cache the toolchain reads.
func (r *run) onBase(ref, cachePath, cacheKey, cacheEnv string) *dagger.Container {
	ctr := r.laneBase(ref).
		WithMountedCache(cachePath, dag.CacheVolume(cacheKey))
	if cacheEnv != "" {
		ctr = ctr.WithEnvVariable(cacheEnv, cachePath)
	}
	return ctr
}

// releaseStep runs one release step and reads it: the container after it, and the
// verdict it settles (releasekit.StepVerdict — the tree's failure is a finding,
// the substrate's and the run's a could-not-run). The step is named in the
// reason, so a failure says which of the record's steps it was.
func releaseStep(ctx context.Context, a checks.AtomDef, ctr *dagger.Container, argv []string) (*dagger.Container, checks.Verdict) {
	ctr = ctr.WithExec(argv, anyExit)
	out, code, err := output(ctx, ctr)
	if err != nil {
		return ctr, checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	v, _ := releasekit.StepVerdict(a, argv, code, out)
	return ctr, v
}

// tsReleaseBuild is the bun release: the tree COPIED into the container (not
// mounted — what the steps write has to be in the container to be read
// back), without a release/ a local `just image` left behind and without
// node_modules, then the frozen install, then the repo's release script. The
// first failure stops the chain, so its verdict names the step that failed.
func (r *run) tsReleaseBuild(ctx context.Context, a checks.AtomDef, ref string) (*dagger.Container, checks.Verdict) {
	tree := r.src.Filter(dagger.DirectoryFilterOpts{Exclude: releasekit.TSExcludes})
	ctr := r.onBase(ref, "/root/.bun/install/cache", "foundry-bun", "").
		WithDirectory(checks.ReleaseTree, tree).
		WithWorkdir(checks.ReleaseTree)
	ctr, v := releaseStep(ctx, a, ctr, releasekit.TSInstall)
	if v.State != 0 {
		return nil, v
	}
	return releaseStep(ctx, a, ctr, checks.TSReleaseArgs)
}

// pythonReleaseBuild is the python release: the tree copied to /app on the
// image's base, without a local .venv or release/, then the sync that builds
// /app/.venv from the star's lock (checks.PythonReleaseArgs).
func (r *run) pythonReleaseBuild(ctx context.Context, a checks.AtomDef, ref string) (*dagger.Container, checks.Verdict) {
	tree := r.src.Filter(dagger.DirectoryFilterOpts{Exclude: releasekit.PythonExcludes})
	ctr := r.onBase(ref, "/opt/uv-cache", "foundry-uv", "UV_CACHE_DIR").
		WithDirectory(checks.PythonReleaseApp, tree).
		WithWorkdir(checks.PythonReleaseApp)
	return releaseStep(ctx, a, ctr, checks.PythonReleaseArgs())
}

// releaseOnBase is Release()'s half for the two base-built releases: the
// directory the build lane stages at release/ — the bun steps' release/ as
// they left it, or the venv under app/.venv for `COPY release/app /app` — and
// whether the image is on one of those bases at all. Not on either, the
// directory is nil and Release() goes on to the compiled lanes; on one, a
// release that did not build is an error, never an empty directory. The
// second answer is the Dockerfile that asked for release/ ("" when none did),
// so the compiled lanes read their binaries off the same file.
func (r *run) releaseOnBase(ctx context.Context) (*dagger.Directory, string, error) {
	files, err := r.population(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("the tree could not be read: %w", err)
	}
	path, body, err := releasekit.ReleaseDockerfile(ctx, runTree{r}, releasekit.Dockerfiles(files))
	if err != nil || path == "" {
		return nil, "", err
	}
	ref, _, _ := buildlane.RuntimeBase(body)
	var (
		ctr *dagger.Container
		v   checks.Verdict
		out func() *dagger.Directory
	)
	switch buildlane.BaseToolchain(ref) {
	case "bun":
		ctr, v = r.tsReleaseBuild(ctx, checks.AtomByID("ts:release"), ref)
		out = func() *dagger.Directory { return ctr.Directory(checks.ReleaseTree + "/" + buildlane.ReleaseDir) }
	case "python":
		ctr, v = r.pythonReleaseBuild(ctx, checks.AtomByID("python:release"), ref)
		out = func() *dagger.Directory {
			return dag.Directory().WithDirectory("app/.venv", ctr.Directory(checks.PythonReleaseApp+"/.venv"))
		}
	default:
		return nil, body, nil
	}
	switch v.State {
	case 2:
		return nil, "", fmt.Errorf("the release build did not run: %s", lastLine(v.Reason))
	case 1:
		return nil, "", fmt.Errorf("the release build failed: %s", lastLine(v.Reason))
	}
	return out(), "", nil
}

// releaseBuild is the Go compile itself: one exec per binary, in the go lane,
// off the root module. The container it answers carries /out, which is what F14
// copies onto the base image.
//
// version "" IS THE GATE'S COMPILE and stamps nothing. The build lane passes the
// stamp tag's version, which exists only once the commit it builds does, and so
// compiles with the version linked in (foundry-tools#18676: every star reported
// "dev"). THE TREE ONLY, NO .git (laneRelease): neither compile reads git, and
// go build stamps vcs.revision from it by default.
func (r *run) releaseBuild(ctx context.Context, plan checks.ReleasePlan, version string) (*dagger.Container, error) {
	// The root module is Release()'s releaseLane, which only reaches here for a
	// root go.mod; the atom, which has no lane to lean on, asks releasekit.GoRoot.
	// A VENDORED MODULE IS PROVISIONED BY ITS vendor/, AND NOTHING IS
	// DOWNLOADED (the atom's check says why).
	base := r.laneRelease(checks.ImageGo)
	if plan.Vendored {
		base = inModule(base, ".")
	} else {
		base = goDownload(base, ".")
	}
	module := ""
	if version != "" {
		// go.mod is there (the root-module check above); a body that names no
		// module fails ReleaseStamp's module check below.
		gomod, _, _ := fileIfPresent(ctx, r.src, "go.mod")
		module = checks.GoModulePath(gomod)
		if _, err := checks.ReleaseStamp(module, version); err != nil {
			return nil, err
		}
	}
	ctr := base
	for _, b := range plan.Binaries {
		ctr = ctr.WithEnvVariable("CGO_ENABLED", checks.ReleaseCgo(b)).WithExec(checks.GoReleaseStampedArgs(b, plan.Vendored, module, version), anyExit)
	}
	return ctr, nil
}

// rustReleaseBuild is the compile itself: the provisioned lane under the
// Unstale stamp (cargoFresh), the target directory on the repository's release
// cache volume (checks.RustReleaseTarget says why), one exec per binary that
// copies the binary out of the volume as it finishes. It answers the container
// the binaries are in and the verdict of the last exec that ran — the first
// failure stops the chain, so the verdict names the binary that did not build.
//
// THE STAMP IS WHAT MAKES THE VOLUME SAFE. Every tree of a repo mounts at
// /src, so the workspace's crates have one artifact each in the volume, and
// cargo judges them by mtime: without Unstale a later tree links what an
// earlier one built (checks.Unstale has the measurement). Under it the
// workspace rebuilds and only the registry crates come back Fresh.
func (r *run) rustReleaseBuild(ctx context.Context, a checks.AtomDef, plan checks.ReleasePlan) (*dagger.Container, checks.Verdict) {
	ctr := r.withReleaseCache(r.cargoFresh()).WithEnvVariable("CARGO_TARGET_DIR", checks.RustReleaseTarget)
	v := checks.VerdictOf(a, 0, "")
	for _, b := range plan.Binaries {
		ctr = ctr.WithExec(copiedOut(checks.RustReleaseArgs(b), checks.RustReleaseBuilt(b)+"="+checks.RustReleaseBinary(b)), anyExit)
		if v = cargoVerdict(ctx, a, ctr); v.State != 0 {
			return ctr, v
		}
	}
	return ctr, v
}

// rustReleaseDir is the release build's binaries as a directory — what F14
// copies onto the base image: each binary read from where its build copied
// it out of the volume (checks.RustReleaseBinary), under its own name at the
// root. Lazy, as the
// Go path's ctr.Directory(ReleaseOut) is: a binary the build did not leave
// (a record naming a package that builds no [[bin]]) surfaces where the
// directory is first used — the image build's context — as the engine's own
// "no such file" for that path, which the build lane files as findings.
func rustReleaseDir(ctr *dagger.Container, plan checks.ReleasePlan) *dagger.Directory {
	out := dag.Directory()
	for _, b := range plan.Binaries {
		out = out.WithFile(b.Name, ctr.File(checks.RustReleaseBinary(b)))
	}
	return out
}
