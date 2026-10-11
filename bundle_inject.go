package main

import (
	"context"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// inject reads what the fleet die serves beside the roster (slag-by-kind F8)
// — the catalog entries, fleet/stars/<n>.json, and foundry/flux's star and
// data/ manifests — and hands them to bundlelane.Inject, which decides
// data.fleet.declared and data.fleet.flux.
//
// FLUX IS PINNED: main is resolved to one commit first, every manifest is
// read at that commit, and the die names it (data.fleet.flux_source), so two
// reads in one build cannot straddle a flux landing and a consumer can tell
// which flux a fact came from. A flux change alone republishes nothing
// (bundlelane.Publishes); the next fleet/ landing carries it.
func (l *bundleLane) inject(ctx context.Context, data string, shards []string) (bundlelane.Injection, gateResult) {
	src := l.m.Source
	bundleSay("── fleet: the catalog entries and the flux facts ──")
	sha, err := dag.Git(checks.FluxRepo).Ref(checks.FluxRef).Commit(ctx)
	if err != nil {
		return bundlelane.Injection{}, couldNotRun("foundry/flux %s could not be resolved to a commit: %v", checks.FluxRef, err)
	}
	l.fluxSHA = sha
	l.flux = dag.Git(checks.FluxRepo).Commit(sha).Tree()
	for _, p := range []string{bundlelane.DeclaredPath, bundlelane.FluxPath, bundlelane.FluxSourcePath} {
		_, ok, err := fileIn(ctx, src, p)
		if err != nil {
			return bundlelane.Injection{}, couldNotRun("%s could not be probed: %v", p, err)
		}
		if ok {
			return bundlelane.Injection{}, findings(p + " is committed, and the lane writes it — the injected key would be overwritten without anyone seeing")
		}
	}
	in := bundlelane.Inputs{FluxSHA: sha, Tier: data, Shards: shards}
	for _, read := range []struct {
		dir     *dagger.Directory
		pattern string
		into    *map[string]string
	}{
		{src, bundlelane.DeclaredGlob, &in.Entries},
		{l.flux, bundlelane.StarManifestGlob, &in.StarManifests},
		{l.flux, bundlelane.ClusterGlob, &in.ClusterManifest},
	} {
		files, g := readGlob(ctx, read.dir, read.pattern)
		if g.code != buildlane.Clean {
			return bundlelane.Injection{}, g
		}
		*read.into = files
	}
	inj, err := bundlelane.Inject(in)
	if err != nil {
		return bundlelane.Injection{}, findings(err.Error())
	}
	bundleSay("fleet: %d catalog entries; flux %.12s states %d of %d known stars (%d CNPG clusters)", inj.DeclaredRows, sha, inj.FluxRows, inj.Known, inj.Clusters)
	return inj, clean()
}

// readGlob reads every file a pattern matches in dir, path -> body.
func readGlob(ctx context.Context, dir *dagger.Directory, pattern string) (map[string]string, gateResult) {
	paths, err := dir.Glob(ctx, pattern)
	if err != nil {
		return nil, couldNotRun("%s could not be listed: %v", pattern, err)
	}
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		body, err := dir.File(p).Contents(ctx)
		if err != nil {
			return nil, couldNotRun("%s could not be read: %v", p, err)
		}
		out[p] = body
	}
	return out, clean()
}
