package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE ATOMS ARE DAGGER CHAINS, NOT SHELL. Rob, 2026-09-12: "It's dagger. It
// has an SDK with native integration with nearly every language we use.
// Atoms should be authored as dagger modules." Until then every atom was a
// POSIX script in a Go string, run through ONE `sh -c` — which handed the
// engine an opaque step it could neither cache in parts nor schedule, and
// re-provisioned every toolchain on every run (measured: `go mod download`
// five times per gate, zero cache volumes, 48 atoms strictly serial).
//
// This file is the runtime every typed atom is built from. The rules it sets,
// and that atoms_*.go follow:
//
//  1. PROVISIONING IS ITS OWN WithExec WITH THE DEFAULT Expect. A failure is a
//     Dagger error, and verdict() files it as state 2 — could not run — with
//     the error text. There is no guard() and there is no `|| exit 2`.
//  2. THE TOOL RUN IS THE LAST EXEC, WITH anyExit. Its exit code reaches the
//     verdict: 0 pass, 1 findings, anything else could-not-run
//     (checks.StateFor). A tool whose own codes mean something else is
//     remapped by a pure function in internal/checks, never by a case
//     statement in shell.
//  3. ABSENCE IS DECIDED IN GO FROM THE DIRECTORY, before any container runs.
//  4. FILE LISTS ARE COMPUTED IN GO (population) AND PASSED AS ARGUMENTS.
//  5. FETCHED TOOLS COME THROUGH dag.HTTP, mirror first, upstream second.
//  6. A SCRIPT THAT IS THE TOOL (foundry-stocks' python and bash) stays the
//     tool: one exec per script, or one per phase.
//  7. NO `sh -c` ANYWHERE. A pipe is a sign the rest belongs in Go.
//  8. GATE_BASE REACHES ONLY THE ATOMS THAT READ IT, so every other atom's
//     cache key is a function of the tree alone, not of the pull.

// anyExit lets the tool's own exit code reach the verdict instead of failing
// the chain. Provisioning steps do NOT take it.
var anyExit = dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny}

// run is one Verdicts call — or one `+check` call — over one tree: the
// repository under check, the change set's base the door named, and the two
// shared trees (foundry-stocks, foundry-dies) built ONCE so every atom that
// mounts them shares the fetch.
type run struct {
	src  *dagger.Directory
	base string

	stocks *dagger.Directory
	dies   *dagger.Directory
}

func newRun(src *dagger.Directory, base string) *run {
	return &run{
		src:  src,
		base: base,
		// The canonical scripts and rulesets, READ AT THEIR ONE HOME rather than
		// vendored (checks.StocksRepo says why). Lazy: nothing is fetched until
		// an atom mounts it.
		stocks: dag.Git(checks.StocksRepo).Ref(checks.StocksRef).Tree(),
		// The fleet's record tree, for the atoms that grade the fleet rather
		// than the repo under test.
		dies: dag.Git(checks.DiesRepo).Ref(checks.DiesRef).Tree(),
	}
}

// lane is the container every atom starts from: the pinned lane image, the
// environment the fleet's toolchains need, the toolchain caches for that
// image, and the tree under check mounted at /src.
//
// THE CACHES ARE THE POINT. checks.CachesFor names, per image, the directories
// its toolchain writes to — go's module and build caches, uv's cache, cargo's
// registry, bun's install cache — and each is a Dagger cache volume that
// persists on the engine across runs, seeded on first creation from the
// image's own warm layer. A gate's second run downloads nothing it downloaded
// on its first.
func (r *run) lane(image string) *dagger.Container {
	ctr := dag.Container().From(image).
		// worktree-guard and every other hook that stands down under CI reads
		// this. The engine IS the CI boundary; saying so beats each atom
		// guessing.
		WithEnvVariable("CI", "true").
		// The go command's coordinates, on every lane rather than only the go
		// lane's: three variables that mean nothing to a non-Go toolchain cost
		// nothing, and a conditional here would be a second place for this
		// file and the atom table to disagree (checks.GoProxy has the
		// measurement).
		WithEnvVariable("GOPROXY", checks.GoProxy).
		WithEnvVariable("GONOSUMDB", checks.GoNoSumDB).
		WithEnvVariable("GOPRIVATE", checks.GoPrivate)
	for _, c := range checks.CachesFor(image) {
		opts := dagger.ContainerWithMountedCacheOpts{}
		if c.Seed {
			opts.Source = dag.Container().From(image).Directory(c.Path)
		}
		ctr = ctr.WithMountedCache(c.Path, dag.CacheVolume(c.Key), opts)
		if c.EnvVar != "" {
			ctr = ctr.WithEnvVariable(c.EnvVar, c.Path)
		}
	}
	return ctr.
		WithMountedDirectory("/src", r.src).
		WithWorkdir("/src")
}

// withStocks mounts foundry-stocks at /stocks — the rulesets and the scripts
// that ARE some atoms' tool. checks.RulesetsDir and the script paths are
// spelled against this mount.
func (r *run) withStocks(ctr *dagger.Container) *dagger.Container {
	return ctr.WithMountedDirectory("/stocks", r.stocks)
}

// withDies mounts foundry-dies at /dies and names it in the environment: the
// tests that read it live in other repositories, and two spellings of one
// path is the shape this helper exists to end.
func (r *run) withDies(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithMountedDirectory("/dies", r.dies).
		WithEnvVariable("FOUNDRY_DIES", "/dies")
}

// withBase hands an atom the change set's base. ONLY the atoms that judge the
// change (fleet:witness, the mutation lane) call this — on every other atom
// it would make the cache key a function of the pull rather than of the tree.
func (r *run) withBase(ctr *dagger.Container) *dagger.Container {
	return ctr.WithEnvVariable("GATE_BASE", r.base)
}

// population is THE GATE'S OWN POPULATION: the files the repository would
// commit, minus what the fleet excludes, matching the patterns given (every
// file when none is given). Patterns are Dagger globs — "**/*.yml".
//
// NO git IS INVOLVED. The engine's own gitignore filter yields the same set
// `git ls-files -o --exclude-standard` did, without the forty-line prelude
// that used to build a throwaway repository so git could be asked. The fleet
// exclude (checks.GatePopulation) is applied in Go, where it is tested.
func (r *run) population(ctx context.Context, patterns ...string) ([]string, error) {
	tracked := r.src.Filter(dagger.DirectoryFilterOpts{
		Gitignore: true,
		Exclude:   []string{".git"},
	})
	if len(patterns) == 0 {
		patterns = []string{"**"}
	}
	seen := map[string]bool{}
	var files []string
	for _, p := range patterns {
		matches, err := tracked.Glob(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("could not enumerate the tree for %q: %w", p, err)
		}
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				files = append(files, m)
			}
		}
	}
	sort.Strings(files)
	return checks.GatePopulation(files), nil
}

// verdict is the one place a chain becomes a verdict. It evaluates the chain:
// a provisioning exec that failed, an image that would not pull, an engine
// that went away all surface as the error — state 2, could not run, never a
// pass. Otherwise the last exec's exit code is the state and its output is
// the result.
func verdict(ctx context.Context, a checks.AtomDef, ctr *dagger.Container) checks.Verdict {
	code, err := ctr.ExitCode(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("the atom never ran: %v", err))
	}
	stdout, _ := ctr.Stdout(ctx)
	stderr, _ := ctr.Stderr(ctx)
	return checks.VerdictOf(a, code, stdout+stderr)
}

// output evaluates a chain whose last exec is a QUESTION rather than the
// verdict — a count, a listing — and answers what it printed and how it
// exited. The last exec must carry anyExit. THE TWO FAILURES ARE DISTINCT:
// an error is the engine's (a mount that would not evaluate, an image that
// would not pull) and is the caller's state 2; a non-zero code is the
// tool's, and what it means is the caller's to decide.
func output(ctx context.Context, ctr *dagger.Container) (stdout string, code int, err error) {
	code, err = ctr.ExitCode(ctx)
	if err != nil {
		return "", 0, err
	}
	stdout, err = ctr.Stdout(ctx)
	if err != nil {
		return "", 0, err
	}
	if code != 0 {
		stderr, _ := ctr.Stderr(ctx)
		stdout += stderr
	}
	return strings.TrimSpace(stdout), code, nil
}
