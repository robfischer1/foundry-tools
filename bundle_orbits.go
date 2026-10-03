package main

import (
	"context"
	"path"
	"slices"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/orbitcompose"
)

// THE ORBIT DIES, ON THE BUNDLE LANE. data/contracts and data/orbits
// (internal/bundlelane orbits.go says what each carries) are built from
// foundry-dies/orbits at the commit that landed, gated by the composition
// itself, and published beside the policy and the roster, under the same
// immutable pin, the same channel move and the same cosign key. This lane
// rather than a new one because it is ALREADY the one that runs on the
// contracts repo's tip with the registry and signing secrets in hand: the die
// was frozen for two months because nothing recomposed it on a merge
// (dies.toml), and a lane that already fires on every merge is the fix.
//
// NOT COMMITTED ANYWHERE. Both payloads are composed here, in memory, from
// the contracts — never a directory a person could edit in a second repo.

// orbitPayloads reads the contracts directory out of the tree and composes
// both dies. A directory that does not compose is a finding about the tree.
func (l *bundleLane) orbitPayloads(ctx context.Context) (contracts, orbits map[string][]byte, g gateResult) {
	bundleSay("── orbits GATE: every contract composes ──")
	src := l.m.Source
	paths, err := src.Glob(ctx, "orbits/*")
	if err != nil {
		return nil, nil, couldNotRun("orbits/ could not be listed: %v", err)
	}
	files := map[string][]byte{}
	for _, p := range paths {
		body, err := src.File(p).Contents(ctx)
		if err != nil {
			return nil, nil, couldNotRun("%s could not be read: %v", p, err)
		}
		files[path.Base(p)] = []byte(body)
	}
	contracts, orbits, err = orbitcompose.Payloads(files)
	if err != nil {
		return nil, nil, findings("foundry-dies/orbits does not compose, so neither orbit die can be built: " + err.Error())
	}
	bundleSay("orbits: %d contracts compose to %d files of data/orbits", len(contracts), len(orbits))
	return contracts, orbits, clean()
}

// publishOrbits pushes both orbit dies under pin, moves each :stable, and
// signs both digests.
func (l *bundleLane) publishOrbits(ctx context.Context, built *dagger.Container, pin string, contracts, orbits map[string][]byte) ([]string, gateResult) {
	var refs []string
	for _, d := range []struct {
		die, stage string
		files      map[string][]byte
	}{
		{bundlelane.ContractsDie, "die-contracts", contracts},
		{bundlelane.OrbitsDie, "die-orbits", orbits},
	} {
		ref, g := l.pushTree(ctx, built, d.die, pin, workDir+"/"+d.stage, d.files)
		if g.code != buildlane.Clean {
			return nil, g
		}
		refs = append(refs, ref)
	}
	cosign, g := l.cosign(ctx)
	if g.code != buildlane.Clean {
		return nil, g
	}
	for _, ref := range refs {
		if g := signVerify(ctx, cosign, ref); g.code != buildlane.Clean {
			return nil, g
		}
	}
	return refs, clean()
}

// pushTree stages files in dir and pushes them as one tree die under die:pin
// unless that pin already resolves, then points :stable at it.
func (l *bundleLane) pushTree(ctx context.Context, built *dagger.Container, die, pin, dir string, files map[string][]byte) (string, gateResult) {
	bundleSay("── %s: publish — immutable pin first, then move the channel ──", die)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	stage := dag.Directory()
	for _, n := range names {
		stage = stage.WithNewFile(n, string(files[n]))
	}
	oras, g := l.oras(ctx, built.WithDirectory(dir, stage))
	if g.code != buildlane.Clean {
		return "", g
	}
	oras = oras.WithWorkdir(dir)
	ref := die + ":" + pin
	_, code, g := exec(ctx, oras, "oras manifest fetch "+ref, "oras", "manifest", "fetch", "--registry-config", "/run/docker/config.json", ref)
	if g.code != buildlane.Clean {
		return "", g
	}
	if code == 0 {
		bundleSay("%s already stands — a pin is immutable, not re-pushing", ref)
		return l.moveChannel(ctx, oras, die, ref, "stable")
	}
	out, code, g := exec(ctx, oras, "oras push "+ref, bundlelane.TreePushArgs(ref, l.m.Sha, names)...)
	if g.code != buildlane.Clean {
		return "", g
	}
	if code != 0 {
		c, why := buildlane.ToolFailed("oras push "+ref, out)
		return "", gateResult{c, why}
	}
	bundleSay("pushed %s (%d files)", ref, len(names))
	return l.moveChannel(ctx, oras, die, ref, "stable")
}
