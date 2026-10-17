package main

import (
	"context"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/visuallane"
)

// visualLane is ts:visual's environment: the ts lane with the lockfile's
// Playwright browsers installed BEFORE the tree is mounted, so that layer is
// keyed on the image and the version alone and a new commit downloads no
// Chromium. The install is provisioning, under the default Expect: a failed one
// is the substrate's. A lockfile that names no (or two) Playwright installs
// nothing, and the atom says why. The pod has no such step: its Playwright
// image carries the browsers.
func visualLane(r *run) *dagger.Container {
	ctr := r.laneBase(checks.ImageTS).
		WithEnvVariable("PLAYWRIGHT_BROWSERS_PATH", "/ms-playwright").
		WithEnvVariable("TURBO_TELEMETRY_DISABLED", "1")
	// The lane is built before any context is in reach; the lockfile read is one
	// small file the engine already holds.
	lock, err := r.src.File("bun.lock").Contents(context.Background())
	if err == nil {
		if version, err := visuallane.PlaywrightVersion(lock); err == nil {
			ctr = ctr.WithExec([]string{"bunx", "playwright@" + version, "install", "--with-deps", "chromium"})
		}
	}
	return ctr.WithMountedDirectory("/src", r.src).WithWorkdir("/src")
}

// visualBind hands the atom the commit its artifact is tagged by and, when the
// Dagger runner's static login (--artifact-auth), the one thing it pushes with:
// the pod has no login and stages its layers for the push-visual phase. The atom
// blanks both for everything the tree runs.
func visualBind(_ context.Context, r *run, ctr *dagger.Container) *dagger.Container {
	if r.sha != "" {
		ctr = ctr.WithEnvVariable("ATOM_SHA", r.sha)
	}
	if r.artifactAuth != nil {
		ctr = ctr.WithSecretVariable("VISUAL_ARTIFACT_AUTH", r.artifactAuth)
	}
	return ctr
}

// withArtifacts hands the run the registry credential the visual atom pushes
// its artifact with, and the commit it is tagged by. Nil pushes nothing.
func (r *run) withArtifacts(auth *dagger.Secret, sha string) *run {
	r.artifactAuth, r.sha = auth, sha
	return r
}

// withRegistrySvid arms the artifact push to log in with the lane's JWT-SVID
// for audience, in place of --artifact-auth, when the run also holds the
// socket. Empty leaves the push as it was.
func (r *run) withRegistrySvid(audience string) *run {
	r.registrySvid = audience
	return r
}
