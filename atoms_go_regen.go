package main

import (
	"context"
	"path"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// withGravityRegen holds a tree's gravity-generated packages to regenerate
// byte-identical (F6, foundry-tools#15441). A package opts in by being one and
// carrying the test: its provenance.json records a generator command that starts
// with `gravity` and the commit that built it, and its directory has a
// regen_test.go (checks.GravityRegens says why both). For those, and only those,
// the lane
//
//   - builds gravity at the recorded commit (`cargo install --locked --git
//     --rev`, the command the regen test's own failure message gives a human) in
//     a rust container and puts the binary on PATH, and
//   - sets <PACKAGE>_REGEN_REQUIRED=1, so the regenerate-and-diff test that
//     skips when no gravity is on PATH fails instead.
//
// WHY BUILT AT THE REV AND NOT THE CAST. app/gravity:stable moves with gravity's
// main, and the test refuses a binary whose --version does not carry the commit
// provenance.json pins (gravity main is already past f450dd4). A cast tag names a
// cast, not a commit, and pulling one needs the registry's credentials in a lane
// that has none. The build is one engine-cached exec keyed on the rev, so it
// compiles once per pin and every later run of every star pinned there reuses
// the layer.
//
// A tree with no gravity-generated package is returned untouched and the scope
// line says so, so a Go star outside this pays nothing and changes nothing.
// A second return is non-empty when the tree's provenance cannot be honoured;
// the caller files it as a finding.
func (r *run) withGravityRegen(ctx context.Context, ctr *dagger.Container) (*dagger.Container, string, string) {
	paths, err := r.src.Glob(ctx, "**/provenance.json")
	if err != nil {
		return ctr, "", "the tree's provenance.json files could not be listed: " + err.Error()
	}
	files := map[string]string{}
	for _, p := range paths {
		body, ok, err := fileIfPresent(ctx, r.src, p)
		if err != nil || !ok {
			return ctr, "", "could not read " + p
		}
		files[path.Clean(p)] = body
	}
	tests, err := r.src.Glob(ctx, "**/"+checks.RegenTestFile)
	if err != nil {
		return ctr, "", "the tree's regen tests could not be listed: " + err.Error()
	}
	regens, err := checks.GravityRegens(files, tests)
	if err != nil {
		return ctr, "", err.Error()
	}
	rev, err := checks.GravityRev(regens)
	if err != nil {
		return ctr, "", err.Error()
	}
	if len(regens) == 0 {
		return ctr, checks.GravityScope(nil, ""), ""
	}
	ctr = ctr.WithFile("/usr/local/bin/gravity", gravityAt(rev), dagger.ContainerWithFileOpts{Permissions: 0o755})
	for _, g := range regens {
		ctr = ctr.WithEnvVariable(g.Env, "1")
	}
	return ctr, checks.GravityScope(regens, rev), ""
}

// gravityAt builds the gravity binary at one commit of the fleet's fork.
func gravityAt(rev string) *dagger.File {
	return dag.Container().From(checks.ImageRust).
		WithExec([]string{"cargo", "install", "--locked", "--git", checks.GravityGit, "--rev", rev,
			"--root", "/opt/gravity", "arcjet-gravity"}).
		WithExec([]string{"/opt/gravity/bin/gravity", "--version"}).
		File("/opt/gravity/bin/gravity")
}
