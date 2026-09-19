package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE BUN AND PYTHON RELEASES (CA master-plan F17). go:release and
// rust:release compile a binary; a bun star's artifact is its bundle and a
// python star's its venv. Both are built ON THE IMAGE'S OWN BASE — the base
// the Dockerfile's runtime stage FROMs — so the runtime the artifact was made
// with is the one that runs it (checks.PythonReleaseApp says what goes wrong
// otherwise). The build lane stages the result at release/ exactly as it
// stages a binary (build.go stageRelease), and the Dockerfile is a FROM and a
// COPY.
//
// THE IMAGE'S BASE DECIDES WHICH OF THEM RUNS. A tree can declare two
// toolchains — mnemosyne carries a go.mod and the pyproject its Go port left
// behind, on the go base — and planning runs every lane the tree declares.
// So each of these atoms stands down unless the Dockerfile that asks for
// release/ is on its own base (buildlane.BaseToolchain), and the go and rust
// atoms, which predate this, are never asked about an image on the bun or
// python base because no such tree carries a go.mod or a Cargo.toml.

func init() {
	register("ts:release", tsRelease)
	register("python:release", pythonRelease)
}

// releaseBase is the release question every release atom asks first, for the
// two that build on the image's base: whether this is a star image, whether
// its Dockerfile asks for release/, and whether that Dockerfile is on the
// base named `want`. It answers the base reference to build on, or the
// verdict that stands the atom down — an absence where the image is not this
// atom's, a could-not-run where the question itself could not be answered.
func (r *run) releaseBase(ctx context.Context, a checks.AtomDef, want string) (string, *checks.Verdict) {
	stand := func(state int, why string) (string, *checks.Verdict) {
		v := checks.VerdictOf(a, state, why)
		return "", &v
	}
	files, err := r.population(ctx)
	if err != nil {
		return stand(2, a.ID+": CANNOT RUN - the tree could not be read: "+err.Error())
	}
	dockerfiles := checks.DockerfilePopulation(files)
	if len(dockerfiles) == 0 {
		return stand(0, a.ID+": ABSENT - this repository tracks no Dockerfile or Containerfile, so it ships no image and has no release build")
	}
	if _, err := r.starName(ctx); err != nil {
		if !isNotAStar(err) {
			return stand(2, a.ID+": CANNOT RUN - "+err.Error())
		}
		return stand(0, a.ID+": ABSENT - "+err.Error()+", so this is not a star image and there is no star release build")
	}
	path, body, err := r.releaseDockerfile(ctx, dockerfiles)
	if err != nil {
		return stand(2, a.ID+": CANNOT RUN - "+err.Error())
	}
	if path == "" {
		return stand(0, a.ID+": ABSENT - no tracked Dockerfile copies from "+buildlane.ReleaseDir+"/, so this image builds itself: its build is the build lane's, and there is no release build to make here")
	}
	ref, _, ok := buildlane.RuntimeBase(body)
	if !ok || buildlane.BaseToolchain(ref) != want {
		on := ref
		if !ok {
			on = "a base this lane cannot name"
		}
		return stand(0, a.ID+": ABSENT - "+path+" is built on "+on+", not "+buildlane.FleetBases+want+", so its release is another lane's")
	}
	return ref, nil
}

// releaseDockerfile is the first tracked Dockerfile that asks for release/,
// with its body — "" when none asks. A Dockerfile that cannot be read is an
// error, never "does not ask" (asksForRelease's rule).
func (r *run) releaseDockerfile(ctx context.Context, dockerfiles []string) (string, string, error) {
	for _, p := range dockerfiles {
		body, err := r.src.File(p).Contents(ctx)
		if err != nil {
			return "", "", fmt.Errorf("%s could not be read: %w", p, err)
		}
		if buildlane.CopiesRelease(body) {
			return p, body, nil
		}
	}
	return "", "", nil
}

// releaseRecord is the star's record, or "" when it has none or it cannot be
// read — the readers treat both as silence (checks.buildOf).
func (r *run) releaseRecord(ctx context.Context) string {
	star, err := r.starName(ctx)
	if err != nil {
		return ""
	}
	slag, err := r.dies.File("fleet/stars/" + star + "/slag.json").Contents(ctx)
	if err != nil {
		return ""
	}
	return slag
}

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
// verdict it settles (checks.ReleaseStepState — the tree's failure is a
// finding, the substrate's and the run's a could-not-run). The step is named
// in the reason, so a failure says which of the record's steps it was.
func releaseStep(ctx context.Context, a checks.AtomDef, ctr *dagger.Container, argv []string) (*dagger.Container, checks.Verdict) {
	ctr = ctr.WithExec(argv, anyExit)
	out, code, err := output(ctx, ctr)
	if err != nil {
		return ctr, checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	state := checks.ReleaseStepState(code, out)
	if state == 0 {
		return ctr, checks.VerdictOf(a, 0, out)
	}
	return ctr, checks.VerdictOf(a, state, fmt.Sprintf("`%s` exited %d\n%s", strings.Join(argv, " "), code, out))
}

// ---- ts:release ----

// The record's release steps leave the image's files under release/, run on
// the image's own bun base.
func tsRelease(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("ts:release")
	ref, stood := r.releaseBase(ctx, a, "bun")
	if stood != nil {
		return *stood
	}
	steps := checks.TSReleaseSteps(r.releaseRecord(ctx))
	if len(steps) == 0 {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the Dockerfile copies from "+buildlane.ReleaseDir+"/ on the bun base, and the star's record names no tools.build.release — a bun release has no convention, so the record has to say how its bundle is made")
	}
	ctr, v := r.tsReleaseBuild(ctx, a, ref, steps)
	if v.State != 0 {
		return v
	}
	made, err := ctr.Directory(checks.ReleaseTree + "/" + buildlane.ReleaseDir).Entries(ctx)
	if err != nil || len(made) == 0 {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - the release steps ran clean and left nothing under "+buildlane.ReleaseDir+"/ — the Dockerfile's COPY would have nothing to copy")
	}
	v.Reason = fmt.Sprintf("release build: %d step(s) from tools.build.release on %s; release/ holds %s\n%s", len(steps), ref, strings.Join(made, ", "), v.Reason)
	return v
}

// tsReleaseBuild is the bun release: the tree COPIED into the container (not
// mounted — what the steps write has to be in the container to be read
// back), without a release/ a local `just image` left behind and without
// node_modules, then the frozen install, then each step in order. The first
// failure stops the chain, so its verdict names the step that failed.
func (r *run) tsReleaseBuild(ctx context.Context, a checks.AtomDef, ref string, steps [][]string) (*dagger.Container, checks.Verdict) {
	tree := r.src.Filter(dagger.DirectoryFilterOpts{Exclude: []string{buildlane.ReleaseDir, "**/node_modules"}})
	ctr := r.onBase(ref, "/root/.bun/install/cache", "foundry-bun", "").
		WithDirectory(checks.ReleaseTree, tree).
		WithWorkdir(checks.ReleaseTree)
	ctr, v := releaseStep(ctx, a, ctr, []string{"bun", "install", "--frozen-lockfile"})
	for _, s := range steps {
		if v.State != 0 {
			return ctr, v
		}
		ctr, v = releaseStep(ctx, a, ctr, s)
	}
	return ctr, v
}

// ---- python:release ----

// The venv the image will carry builds from the star's lock on the image's
// own python base.
func pythonRelease(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("python:release")
	ref, stood := r.releaseBase(ctx, a, "python")
	if stood != nil {
		return *stood
	}
	extras := checks.PythonExtras(r.releaseRecord(ctx))
	ctr, v := r.pythonReleaseBuild(ctx, a, ref, extras)
	if v.State != 0 {
		return v
	}
	if made, err := ctr.Directory(checks.PythonReleaseApp + "/.venv/bin").Entries(ctx); err != nil || len(made) == 0 {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - uv sync ran clean and left no "+checks.PythonReleaseApp+"/.venv — the Dockerfile's COPY would have no venv to copy")
	}
	with := "no extras"
	if len(extras) > 0 {
		with = "extras " + strings.Join(extras, ", ") + " from tools.build.extras"
	}
	v.Reason = fmt.Sprintf("release build: the venv, with %s, on %s\n%s", with, ref, v.Reason)
	return v
}

// pythonReleaseBuild is the python release: the tree copied to /app on the
// image's base, without a local .venv or release/, then the sync that builds
// /app/.venv from the star's lock (checks.PythonReleaseArgs).
func (r *run) pythonReleaseBuild(ctx context.Context, a checks.AtomDef, ref string, extras []string) (*dagger.Container, checks.Verdict) {
	tree := r.src.Filter(dagger.DirectoryFilterOpts{Exclude: []string{".venv", buildlane.ReleaseDir, "**/__pycache__"}})
	ctr := r.onBase(ref, "/opt/uv-cache", "foundry-uv", "UV_CACHE_DIR").
		WithDirectory(checks.PythonReleaseApp, tree).
		WithWorkdir(checks.PythonReleaseApp)
	return releaseStep(ctx, a, ctr, checks.PythonReleaseArgs(extras))
}

// releaseOnBase is Release()'s half for the two base-built releases: the
// directory the build lane stages at release/ — the bun steps' release/ as
// they left it, or the venv under app/.venv for `COPY release/app /app` — and
// whether the image is on one of those bases at all. Not on either, the
// answer is (nil, false, nil) and Release() goes on to the compiled lanes;
// on one, a release that did not build is an error, never an empty directory.
func (r *run) releaseOnBase(ctx context.Context) (*dagger.Directory, bool, error) {
	files, err := r.population(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("the tree could not be read: %w", err)
	}
	path, body, err := r.releaseDockerfile(ctx, checks.DockerfilePopulation(files))
	if err != nil || path == "" {
		return nil, false, err
	}
	ref, _, _ := buildlane.RuntimeBase(body)
	var (
		ctr *dagger.Container
		v   checks.Verdict
		out func() *dagger.Directory
	)
	switch buildlane.BaseToolchain(ref) {
	case "bun":
		steps := checks.TSReleaseSteps(r.releaseRecord(ctx))
		if len(steps) == 0 {
			return nil, true, fmt.Errorf("the star's record names no tools.build.release, so its bun release has no steps")
		}
		ctr, v = r.tsReleaseBuild(ctx, checks.AtomByID("ts:release"), ref, steps)
		out = func() *dagger.Directory { return ctr.Directory(checks.ReleaseTree + "/" + buildlane.ReleaseDir) }
	case "python":
		ctr, v = r.pythonReleaseBuild(ctx, checks.AtomByID("python:release"), ref, checks.PythonExtras(r.releaseRecord(ctx)))
		out = func() *dagger.Directory {
			return dag.Directory().WithDirectory("app/.venv", ctr.Directory(checks.PythonReleaseApp+"/.venv"))
		}
	default:
		return nil, false, nil
	}
	switch v.State {
	case 2:
		return nil, true, fmt.Errorf("the release build did not run: %s", lastLine(v.Reason))
	case 1:
		return nil, true, fmt.Errorf("the release build failed: %s", lastLine(v.Reason))
	}
	return out(), true, nil
}
