package main

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

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
//
// IT RUNS AFTER THE OTHER TWO DIES SETTLE, on their answer: a lane that
// already failed stays failed and the orbit dies are not touched; a clean one
// gains the orbit dies' own sentence. Every answer here is a gateResult
// carrying a reason, so a success is never the zero value.

// orbitDies gates the contracts and, when the landing touched orbits/,
// publishes both orbit dies. before is what the policy and roster answered.
func (l *bundleLane) orbitDies(ctx context.Context, before gateResult) gateResult {
	if before.code != buildlane.Clean {
		return before
	}
	contracts, orbits, gated := l.orbitPayloads(ctx)
	if gated.code != buildlane.Clean {
		return gated
	}
	note := func(s string) gateResult { return gateResult{before.code, before.reason + "; " + gated.reason + s} }
	if !bundlelane.OrbitsPublish(l.touched) {
		return note(" — nothing under orbits/ changed, data/contracts and data/orbits stay where they are")
	}
	if l.dryRun {
		return note(" — dry run, data/contracts and data/orbits not published")
	}
	published := l.publishOrbits(ctx, contracts, orbits)
	if published.code != buildlane.Clean {
		return published
	}
	return note("; " + published.reason)
}

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
	prefixes, err := roster(ctx, src)
	if err != nil {
		return nil, nil, couldNotRun("the roster beside orbits/ could not be read: %v", err)
	}
	contracts, orbits, err = orbitcompose.Payloads(files, starSet(prefixes))
	if err != nil {
		return nil, nil, findings("foundry-dies/orbits does not compose, so neither orbit die can be built: " + err.Error())
	}
	why := fmt.Sprintf("orbits: %d contracts compose to %d files of data/orbits", len(contracts), len(orbits))
	bundleSay("%s", why)
	return contracts, orbits, gateResult{buildlane.Clean, why}
}

// publishOrbits pushes both orbit dies under the pin from one oras container,
// moves each :stable, and signs both digests. Its reason names what it
// published.
func (l *bundleLane) publishOrbits(ctx context.Context, contracts, orbits map[string][]byte) gateResult {
	// run refused a sha too short for a pin before anything was built, and a
	// refused run never reaches here (orbitDies passes it through).
	pin, _ := bundlelane.Pin(l.m.Sha)
	oras, g := l.oras(ctx, l.built)
	if g.code != buildlane.Clean {
		return g
	}
	var refs []string
	for _, d := range []struct {
		die, dir string
		files    map[string][]byte
	}{
		{bundlelane.ContractsDie, workDir + "/die-contracts", contracts},
		{bundlelane.OrbitsDie, workDir + "/die-orbits", orbits},
	} {
		ref, g := l.pushTree(ctx, oras, d.die, pin, d.dir, d.files)
		if g.code != buildlane.Clean {
			return g
		}
		refs = append(refs, ref)
	}
	cosign, g := l.cosign(ctx)
	if g.code != buildlane.Clean {
		return g
	}
	for _, ref := range refs {
		if g := signVerify(ctx, cosign, ref); g.code != buildlane.Clean {
			return g
		}
	}
	return gateResult{buildlane.Clean, "published and signed " + strings.Join(refs, ", ")}
}

// pushTree writes files into dir and pushes them from there as one tree die
// under die:pin unless that pin already resolves, then points :stable at it.
func (l *bundleLane) pushTree(ctx context.Context, oras *dagger.Container, die, pin, dir string, files map[string][]byte) (string, gateResult) {
	bundleSay("── %s: publish — immutable pin first, then move the channel ──", die)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		oras = oras.WithNewFile(dir+"/"+n, string(files[n]))
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
