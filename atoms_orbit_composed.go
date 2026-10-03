package main

import (
	"context"
	"path"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/orbitcompose"
)

// ops:orbit-composed — FLUX'S RENDERED ORBIT SIDECARS ARE NOT STALE AGAINST
// foundry-dies MAIN.
//
// orbitcompose renders prime/orbits from foundry-dies/orbits, and nothing
// runs it: a contract that changes in the dies reaches a pod only if someone
// re-renders (Task #104.1). This atom makes forgetting LOUD. Every gate on a
// tree that renders sidecars composes the dies' contracts as they stand on
// main and refuses the tree if a re-render would write or remove anything.
//
// WHY THE DIES AT MAIN, NOT AT A PIN. The run already mounts foundry-dies at
// main (newRun), and main is what the composer would read if someone ran it
// now. A flux pull that did not touch prime/orbits can go red after a
// dies change. That is the point: the red names the files and how to re-render
// them, and it stays red until someone does.
//
// SELF-CONTAINED, so it can be lifted as is. The judgement is
// orbitcompose.CheckDrift, a pure function over two maps. This file only
// reads the two directories. An orbit lane of its own (Rob, 2026-10-03:
// "Ultimately want orbit validation to be a CI check/lane of its own") can
// call the same two lines.
func init() {
	register("ops:orbit-composed", opsOrbitComposed)
}

const (
	// orbitRenderDir is the directory orbitcompose owns in the tree under check.
	orbitRenderDir = "prime/orbits"
	// orbitContractsDir is where the contracts live in foundry-dies.
	orbitContractsDir = "orbits"
	// orbitNamespace is the namespace the sidecars' ConfigMaps are rendered
	// for: orbitcompose's own default, and the one flux renders.
	orbitNamespace = "prime"
)

func opsOrbitComposed(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("ops:orbit-composed")
	have, err := filesByName(ctx, r.src, orbitRenderDir+"/*")
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the rendered sidecars could not be read: "+err.Error())
	}
	if len(have) == 0 {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - this tree renders no orbit sidecars (no "+orbitRenderDir+"/)")
	}
	contracts, err := filesByName(ctx, r.dies, orbitContractsDir+"/*.toml")
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - foundry-dies' contracts could not be read: "+err.Error())
	}
	d := orbitcompose.CheckDrift(contracts, have, orbitNamespace)
	return checks.VerdictOf(a, d.State, orbitRenderDir+" against foundry-dies main: "+d.Report)
}

// filesByName reads every file a glob matches in dir, keyed by base name, as
// the composer reads a directory.
func filesByName(ctx context.Context, dir *dagger.Directory, pattern string) (map[string][]byte, error) {
	paths, err := dir.Glob(ctx, pattern)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(paths))
	for _, p := range paths {
		// A DIRECTORY IS NAMED, NOT READ. The engine's glob answers one with
		// its trailing separator, and the composer's directory holds only
		// files: nil bytes make it a file the composer does not own, which
		// CheckDrift reports by name, rather than a read that fails.
		if strings.HasSuffix(p, "/") {
			out[path.Base(p)] = nil
			continue
		}
		raw, err := dir.File(p).Contents(ctx)
		if err != nil {
			return nil, err
		}
		out[path.Base(p)] = []byte(raw)
	}
	return out, nil
}
