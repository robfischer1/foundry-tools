package main

import (
	"context"
	"path"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/dagger"
)

// staged is one injected key's data.json and how many star rows it holds.
type staged struct {
	body string
	rows int
}

// inject reads what the fleet die serves beside the roster (slag-by-kind F8):
// the catalog entries, fleet/stars/<n>.json, as data.fleet.declared, and the
// facts foundry/flux states about each star, as data.fleet.flux. The stars
// flux is read for are the ones the tree knows: a slag shard or a catalog
// entry.
//
// FLUX IS READ AT main WHEN THE DIE IS BUILT, so the facts are as of this
// landing; a flux change alone republishes nothing (bundlelane.Publishes).
// A flux tree that answers no star manifest is a findings, not an empty key:
// an empty data.fleet.flux would derive every address away.
func (l *bundleLane) inject(ctx context.Context, data string, shards []string) (staged, staged, gateResult) {
	src := l.m.Source
	bundleSay("── fleet: the catalog entries and the flux facts ──")
	hit, err := bundlelane.Collides(data)
	if err != nil {
		return staged{}, staged{}, findings(err.Error())
	}
	if len(hit) > 0 {
		return staged{}, staged{}, findings("fleet/data.json already sets data.fleet." + strings.Join(hit, ", data.fleet.") + ", which the lane injects — OPA would refuse the bundle")
	}
	for _, p := range []string{bundlelane.DeclaredPath, bundlelane.FluxPath} {
		if _, ok, err := fileIn(ctx, src, p); err != nil {
			return staged{}, staged{}, couldNotRun("%s could not be probed: %v", p, err)
		} else if ok {
			return staged{}, staged{}, findings(p + " is committed, and the lane writes it — the injected key would be overwritten without anyone seeing")
		}
	}
	paths, err := src.Glob(ctx, bundlelane.DeclaredGlob)
	if err != nil {
		return staged{}, staged{}, couldNotRun("the catalog entries could not be listed: %v", err)
	}
	files, g := contentsOf(ctx, src, paths)
	if g.code != buildlane.Clean {
		return staged{}, staged{}, g
	}
	docs, err := bundlelane.Declared(files)
	if err != nil {
		return staged{}, staged{}, findings(err.Error())
	}

	stars := map[string]bool{}
	for _, s := range shards {
		stars[path.Base(path.Dir(s))] = true
	}
	for name := range docs {
		stars[name] = true
	}
	manifests, err := l.flux.Glob(ctx, bundlelane.StarManifestGlob)
	if err != nil {
		return staged{}, staged{}, couldNotRun("foundry/flux's star manifests could not be listed: %v", err)
	}
	if len(manifests) == 0 {
		return staged{}, staged{}, findings("foundry/flux answered no " + bundlelane.StarManifestGlob + " — an empty data.fleet.flux would derive every star's address away")
	}
	clusterFiles, err := l.flux.Glob(ctx, bundlelane.ClusterGlob)
	if err != nil {
		return staged{}, staged{}, couldNotRun("foundry/flux's data/ could not be listed: %v", err)
	}
	starBodies, g := contentsOf(ctx, l.flux, manifests)
	if g.code != buildlane.Clean {
		return staged{}, staged{}, g
	}
	clusterBodies, g := contentsOf(ctx, l.flux, clusterFiles)
	if g.code != buildlane.Clean {
		return staged{}, staged{}, g
	}
	clusters, err := bundlelane.Clusters(clusterBodies)
	if err != nil {
		return staged{}, staged{}, findings("foundry/flux " + err.Error())
	}
	facts, err := bundlelane.FluxFacts(starBodies, stars, clusters)
	if err != nil {
		return staged{}, staged{}, findings("foundry/flux " + err.Error())
	}

	declaredBody, err := bundlelane.Stage(docs)
	if err != nil {
		return staged{}, staged{}, findings(err.Error())
	}
	fluxBody, err := bundlelane.Stage(facts)
	if err != nil {
		return staged{}, staged{}, couldNotRun("the flux facts would not render: %v", err)
	}
	bundleSay("fleet: %d catalog entries; flux states %d of %d known stars (%d CNPG clusters)", len(docs), len(facts), len(stars), len(clusters))
	return staged{declaredBody, len(docs)}, staged{fluxBody, len(facts)}, clean()
}

// contentsOf reads each path in dir, path -> body.
func contentsOf(ctx context.Context, dir *dagger.Directory, paths []string) (map[string]string, gateResult) {
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
