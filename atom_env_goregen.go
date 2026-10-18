package main

import (
	"context"
	"slices"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE ENVIRONMENT OF dev:go-regen: the rust lane, with the programs the atom
// execs on PATH. It builds a guest with cargo (the compiler a core pins, when
// that is not the lane's, is laid by the atom itself), composes with wasm-tools
// and wac, binds with the gravity prebuilt for the commit the core pins, and
// gravity execs gofmt. These are the tools the pod provisions from tools.toml
// for the same atom (Needs: tool:wasm-tools, tool:wac, tool:gofmt, and every
// gravity-<rev7>), here as the same pinned bytes.

// goRegenTools puts the pinned programs on the container's PATH. A tool that
// will not fetch is left off, and the atom's own probe settles 2 on it.
func goRegenTools(ctx context.Context, r *run, ctr *dagger.Container) *dagger.Container {
	ctr = witTools(true)(ctx, r, ctr)
	for _, rev := range sortedRevs() {
		ctr = ctr.WithFile("/usr/local/bin/"+checks.GravityTool(rev), gravityAt(rev), dagger.ContainerWithFileOpts{Permissions: 0o755})
	}
	// gofmt takes no --version; formatting a package clause proves it runs.
	if tooled, err := withTarballBinaryProbe(ctx, ctr, checks.GofmtURL, checks.GofmtMember, "gofmt", 2, []string{"gofmt"}, "package p\n"); err == nil {
		ctr = tooled
	}
	return ctr
}

// sortedRevs are the gravity commits that have a prebuilt, in order.
func sortedRevs() []string {
	revs := make([]string, 0, len(checks.GravityPrebuilt))
	for rev := range checks.GravityPrebuilt {
		revs = append(revs, rev)
	}
	slices.Sort(revs)
	return revs
}
