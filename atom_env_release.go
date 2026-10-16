package main

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/releasekit"
)

// THE RELEASE ATOMS' ENVIRONMENT. The question each asks is in its atom
// (atoms/<ns>/release); what stays here is the place it is asked in. go:release
// runs in the plain go lane. rust:release runs in the fetched rust lane with
// the repository's release volume as its target directory, which is the
// CARGO_TARGET_DIR the atom honours; the volume is shared and nothing waits on
// it, since the atom builds and carries each binary out in one exec. ts:release
// and python:release run in the base the image's Dockerfile FROMs, so the
// runtime the artifact was made with is the one that runs it.

// rustReleaseLane is the fetched rust lane with the release volume as the
// cargo target directory.
func rustReleaseLane(r *run) *dagger.Container {
	return r.withReleaseVolume(r.cargoDeps()).WithEnvVariable("CARGO_TARGET_DIR", checks.RustReleaseTarget)
}

// baseReleaseLane is the container for a release that builds on the image's own
// base: that base with the toolchain's cache and the tree at /src. When the
// image is not on the base (the atom stands down) it is the toolchain's lane,
// which the atom never builds in.
func baseReleaseLane(id, toolchain, cachePath, cacheKey, cacheEnv string) func(context.Context, *run) *dagger.Container {
	return func(ctx context.Context, r *run) *dagger.Container {
		a := checks.AtomByID(id)
		tree := runTree{r}
		if sub, stood := releasekit.Resolve(ctx, tree, a, releasekit.Origin(r.repo, r.origin), releasekit.Builds); stood == nil {
			if ref, stood := releasekit.OnBase(a, sub, toolchain); stood == nil {
				return r.onBase(ref, cachePath, cacheKey, cacheEnv).
					WithMountedDirectory("/src", r.src).
					WithWorkdir("/src")
			}
		}
		return r.lane(a.Image)
	}
}

// withTreeFacts hands the atom the tree as tree@1: the gate's population, the
// clone the repository came from and nothing of the change. A population that
// would not enumerate rides as the file list's error, as the atom words it.
func (r *run) withTreeFacts(ctx context.Context, ctr *dagger.Container) *dagger.Container {
	tree := atomkit.Tree{Version: 1, Root: "/src", Origin: releasekit.Origin(r.repo, r.origin)}
	files, err := r.population(ctx)
	if err != nil {
		tree.FilesErr = err.Error()
	}
	for _, list := range []*[]string{&tree.Files, &tree.Committable, &tree.Tracked} {
		*list = append([]string{}, files...)
	}
	tree.Changed = []string{}
	// a tree@1 is plain strings and lists, which always marshal
	b, _ := atomkit.Marshal(tree)
	return ctr.WithNewFile("/work/tree.json", string(b)).WithEnvVariable("ATOM_TREE", "/work/tree.json")
}
