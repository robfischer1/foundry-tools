package tsmut

import (
	"context"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/unitkey"
)

// THE SOURCE A CHANGED TEST RUNS joins the scope (checks/testcovered.go says
// why). The *.test.* / *.spec.* files the change touched run alone, per package
// that owns them, under bun's own coverage after the install; every source line
// they executed in a package Stryker can run is handed to it as though it had
// changed. Test files are never mutated.
//
// BUN'S COVERAGE, NOT VITEST'S OR STRYKER'S. vitest needs a coverage provider
// package no fleet lockfile carries, and a frozen install cannot add one;
// Stryker's perTest coverage maps tests to mutants it has already made of the
// whole `mutate` glob, which is the cost this scope exists to avoid. bun is the
// lane's installer and runs the suites' vitest imports itself. A suite bun
// cannot run writes what coverage it has, or none, and adds only that.

// testCovered answers the source lines the change's tests execute, named from
// the root, and the verdict's line for them; nil and "" when no test changed. An
// install that fails is the run's end, as the Stryker run's would be.
func (g *grading) testCovered(ctx context.Context, since string) (map[string][]unitkey.Range, string, *checks.Verdict) {
	s := g.do(ctx, g.in.Root, "git", "diff", "--name-only", "--diff-filter=AM", since, "HEAD")
	if s.code != 0 {
		return nil, id + ": changed tests: git could not list them, so their coverage joined no scope: " + atoms.LastLine(s.out), nil
	}
	var tests []string
	for _, f := range strings.Fields(s.out) {
		if checks.TSTestFile(f) && !strings.Contains(f, "node_modules/") {
			tests = append(tests, f)
		}
	}
	if len(tests) == 0 {
		return nil, "", nil
	}
	if g.in.FilesErr != nil {
		return nil, id + ": changed tests: the tree's Stryker configs could not be listed, so their coverage joined no scope", nil
	}
	dirs := checks.StrykerConfigDirs(atoms.Matching(g.in.Files, "**/*stryker.con*"))
	byPkg := map[string][]string{}
	for _, f := range tests {
		if owner := checks.StrykerOwner(f, dirs); owner != "" {
			rel := f
			if owner != "." {
				rel = strings.TrimPrefix(f, owner+"/")
			}
			byPkg[owner] = append(byPkg[owner], "./"+rel)
		}
	}
	if len(byPkg) == 0 {
		return nil, id + ": changed tests: no Stryker config owns them, so their coverage joined no scope", nil
	}
	if v, ok := g.setup(ctx); !ok {
		return nil, "", &v
	}
	covered := map[string][]unitkey.Range{}
	var notes []string
	for _, pkg := range slices.Sorted(maps.Keys(byPkg)) {
		got, note := g.coverOf(ctx, pkg, byPkg[pkg])
		covered = checks.UnionMutationRanges(covered, got)
		if note != "" {
			notes = append(notes, note)
		}
	}
	covered = checks.KeepCovered(covered, func(f string) bool {
		return g.mutable(f) && checks.StrykerOwner(f, dirs) != ""
	})
	kept, say := checks.CoveredScope(id, "they run", covered)
	return kept, strings.Join(append([]string{say}, notes...), "\n"), nil
}

// mutable is a file the run's scope may take: inside a declared module, or with
// none declared, a source TSMutationSpecs takes.
func (g *grading) mutable(f string) bool {
	mods := strings.Fields(g.mods)
	if len(mods) == 0 {
		return checks.TSUndeclaredSource(f)
	}
	if checks.TSTestFile(f) {
		return false
	}
	for _, m := range mods {
		if len(atoms.Matching([]string{f}, m)) > 0 || checks.UnderAny(f, []string{m}) {
			return true
		}
	}
	return false
}

// coverOf runs one package's changed tests under bun's coverage and answers the
// lines that ran, named from the root.
func (g *grading) coverOf(ctx context.Context, pkg string, tests []string) (map[string][]unitkey.Range, string) {
	dir := filepath.Join(work(), "testcov", strings.ReplaceAll(path.Clean(pkg), "/", "_"))
	args := append([]string{"test", "--coverage", "--coverage-reporter", "lcov", "--coverage-dir", dir}, tests...)
	s := g.do(ctx, filepath.Join(g.tree, pkg), "bun", args...)
	raw, err := readFile(filepath.Join(dir, "lcov.info"))
	if err != nil {
		return nil, fmt.Sprintf("%s: changed tests in %s wrote no coverage (exit %d), so they added nothing: %s", id, pkg, s.code, atoms.LastLine(s.out))
	}
	note := ""
	if s.code != 0 {
		note = fmt.Sprintf("%s: changed tests in %s exited %d under bun; the coverage they wrote was read", id, pkg, s.code)
	}
	return checks.LCOVRanges(string(raw), g.tree, pkg), note
}
