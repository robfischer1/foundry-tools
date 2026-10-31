// Package fleetbunpackagetest is the atom fleet:bun-package-test: a TypeScript package
// that carries tests and no package.json of its own, in a tree with no root package.json,
// has its tests run with bun. cerberus' Pi extension (payload/pi-cerberus) is the case.
package fleetbunpackagetest

import (
	"context"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. Its surface is the test file itself, so a tree with none never
// pays for the bun image.
var Def = atomkit.Def{
	ID: "fleet:bun-package-test", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "A TypeScript package with tests and no package.json, in a tree with no root package.json (so ts:bun-gate is absent), has its tests run by bun test; a tsconfig.json beside such a package is could-not-run, because no tsc is pinned for it.",
	Surface:   "any:**/*.test.ts",
	Toolchain: "bun",
	// The package's tests may shell out to git (cerberus' pi-cerberus git and
	// gitguard tests build throwaway repos); the bun image carries none.
	Needs:   []string{"tool:git"},
	Timeout: 10 * time.Minute,
	Check:   check,
}

const id = "fleet:bun-package-test"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// testGlobs are bun's test-file patterns for TypeScript and JavaScript
// (checks.TSTestPatterns less its extension-free `_test_` forms, which would
// match any language's file).
var testGlobs = func() []string {
	var out []string
	for _, g := range checks.TSTestPatterns {
		if !strings.Contains(g, "_test_") && !strings.Contains(g, "_spec_") {
			out = append(out, g)
		}
	}
	return out
}()

// check finds the package-local test directories and runs bun test over each.
//
// WHAT "PACKAGE-LOCAL" MEANS. A test file whose directory, and every directory above
// it, holds no package.json. A tree with a ROOT package.json is ts:bun-gate's: that
// atom runs the repo's own gate script, which owns every test under it, so this one
// says ABSENT. A test under a nested package.json with no root one is graded by
// nobody (ts:bun-gate looks only at the root); the pass line names those directories,
// because quiet would read as covered. Installing a package's dependencies is a
// different job from running tests that import nothing from a registry, and is not done.
//
// THE TSCONFIG. A tsconfig.json at or above a package-local test directory asks for
// a type check, and the image carries no pinned tsc and a package with no package.json
// names no typescript to install. That is a could-not-run naming the file, not a
// skipped check and not a `bun x tsc` that fetches whatever is latest.
//
// bun test's exit is its verdict: 1 is a failing test, and everything else that is not
// 0 (a missing binary, an OOM kill) is a 2 through checks.VerdictOf.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	files := in.Files
	var tests []string
	for _, g := range testGlobs {
		for _, f := range atoms.Matching(files, g) {
			if !isFixture(f) {
				tests = append(tests, f)
			}
		}
	}
	if len(tests) == 0 {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - this tree holds no TypeScript or JavaScript test file")
	}
	if slices.Contains(files, "package.json") {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - this tree has a root package.json, so ts:bun-gate runs its tests through the repo's own gate script")
	}

	has := func(p string) bool { return slices.Contains(files, p) }
	ancestors := func(d string) []string {
		var out []string
		for {
			out = append(out, d)
			if d == "." {
				return out
			}
			d = path.Dir(d)
		}
	}
	local := map[string]bool{}
	var nested []string
	for _, f := range tests {
		d := path.Dir(f)
		owned := ""
		for _, a := range ancestors(d) {
			if has(path.Join(a, "package.json")) {
				owned = a
				break
			}
		}
		if owned != "" {
			if !slices.Contains(nested, owned) {
				nested = append(nested, owned)
			}
			continue
		}
		local[d] = true
	}
	slices.Sort(nested)
	ungraded := ""
	if len(nested) > 0 {
		ungraded = "; NOT GRADED, each has its own package.json and no root one runs it: " + strings.Join(nested, ", ")
	}
	if len(local) == 0 {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - every test file sits under a package.json of its own"+ungraded)
	}

	dirs := collapse(local)
	for _, d := range dirs {
		for _, a := range ancestors(d) {
			if tsc := path.Join(a, "tsconfig.json"); has(tsc) {
				return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+tsc+
					" asks for a type check of "+d+", and neither the image nor a package.json pins a tsc to run it with")
			}
		}
	}
	for _, d := range dirs {
		args := []string{"test"}
		if d != "." {
			args = append(args, d+"/")
		}
		out, code, bad := atoms.RunTool(ctx, in, row, atoms.Cmd{Dir: in.Root, Name: "bun", Args: args, Both: true})
		if bad != nil {
			return *bad
		}
		if code != 0 {
			return checks.VerdictOf(row, code, id+": bun test "+d+" exited "+strconv.Itoa(code)+"\n"+out)
		}
	}
	return checks.VerdictOf(row, int(checks.StatePass), id+": PASS - bun test ran in "+strings.Join(dirs, ", ")+ungraded)
}

// isFixture is a test-named file that is not a package's test: one under a testdata,
// fixtures, vendor or node_modules directory (a golden tree or a vendored dependency),
// or inside a template directory whose name is a Jinja expression (`{% if x %}__tests__`,
// `{{ name }}`), which is a file copier renders and not one bun can run. Measured on the
// fleet 2026-10-31: frontend-repo-template's template test would have been refused as a
// failing suite, and narcissus' golden tree ran as an empty one.
func isFixture(p string) bool {
	if strings.Contains(p, "{{") || strings.Contains(p, "{%") {
		return true
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "testdata", "fixtures", "vendor", "node_modules":
			return true
		}
	}
	return false
}

// collapse is the directories of local that are not inside another one, sorted:
// bun test takes a path prefix, so the outer directory already runs the inner.
func collapse(local map[string]bool) []string {
	var all []string
	for d := range local {
		all = append(all, d)
	}
	slices.Sort(all)
	var out []string
	for _, d := range all {
		inside := false
		for _, o := range out {
			if o == "." || strings.HasPrefix(d, o+"/") {
				inside = true
			}
		}
		if !inside {
			out = append(out, d)
		}
	}
	return out
}
