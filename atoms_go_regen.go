package main

import (
	"context"
	"errors"
	"fmt"

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
// The error is either a tree whose provenance cannot be honoured, which the
// caller files as a finding, or errGravityRead, the engine failing to show the
// tree, which is a could-not-run.
// errGravityRead marks a failure to read the tree, as opposed to a tree that
// reads and says something unusable.
var errGravityRead = errors.New("the tree's provenance could not be read")

func (r *run) withGravityRegen(ctx context.Context, ctr *dagger.Container) (*dagger.Container, string, error) {
	paths, perr := r.src.Glob(ctx, "**/provenance.json")
	tests, terr := r.src.Glob(ctx, "**/"+checks.RegenTestFile)
	files := map[string]string{}
	var rerr error
	for _, p := range paths {
		body, _, err := fileIfPresent(ctx, r.src, p)
		rerr = errors.Join(rerr, err)
		files[p] = body
	}
	if err := errors.Join(perr, terr, rerr); err != nil {
		return ctr, "", fmt.Errorf("%w: %v", errGravityRead, err)
	}
	plan, err := checks.PlanGravity(files, tests)
	if err != nil {
		return ctr, "", err
	}
	if len(plan.Regens) == 0 {
		return ctr, plan.Scope, nil
	}
	ctr = ctr.WithFile("/usr/local/bin/gravity", gravityAt(plan.Rev), dagger.ContainerWithFileOpts{Permissions: 0o755})
	for _, g := range plan.Regens {
		ctr = ctr.WithEnvVariable(g.Env, "1")
	}
	return ctr, plan.Scope, nil
}

// gravityAt builds the gravity binary at one commit of the fleet's fork.
func gravityAt(rev string) *dagger.File {
	return dag.Container().From(checks.ImageRust).
		WithExec([]string{"cargo", "install", "--locked", "--git", checks.GravityGit, "--rev", rev,
			"--root", "/opt/gravity", "arcjet-gravity"}).
		WithExec([]string{"/opt/gravity/bin/gravity", "--version"}).
		File("/opt/gravity/bin/gravity")
}
