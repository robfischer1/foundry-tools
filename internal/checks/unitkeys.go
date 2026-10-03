package checks

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/unitkey"
)

// THE LANE'S HALF OF A GRADING'S KEY: what it reads off the tree it graded,
// turned into unitkey's inputs. Every function here is pure, so the atoms
// only run the three reads (`git ls-tree`, `git diff -U0`, `go list` or
// `cargo metadata`) and hand their output over.

// GoListDepsFormat is the `go list -e -deps -test -f` template the Go closure
// is read from: each package's import path, directory and transitive imports.
const GoListDepsFormat = `{{.ImportPath}}{{"\t"}}{{.Dir}}{{"\t"}}{{join .Deps " "}}`

// ParseLsTree reads `git ls-tree -r -z --full-tree HEAD` into the tree's
// files. Only blobs are files: a submodule is a commit, and its content is not
// this tree's.
func ParseLsTree(out string) []unitkey.Entry {
	var entries []unitkey.Entry
	for _, rec := range strings.Split(out, "\x00") {
		meta, file, ok := strings.Cut(rec, "\t")
		if f := strings.Fields(meta); ok && len(f) == 3 && f[1] == "blob" {
			entries = append(entries, unitkey.Entry{Path: file, Blob: f[2]})
		}
	}
	return entries
}

// relTo is dir relative to root, "." for root itself, false outside it.
func relTo(root, dir string) (string, bool) {
	if dir == root {
		return ".", true
	}
	rel, ok := strings.CutPrefix(dir, root+"/")
	return rel, ok
}

// GoClosures reads `go list -e -deps -test -f GoListDepsFormat ./...`, run in
// a checkout at root, into each in-repo package directory's test closure: the
// directory itself and the directory of every package its tests link —
// test-variant records included, since a package's test binary is listed as
// `p.test` with p's directory and every test dependency in its Deps. Packages
// outside root (the standard library, the module cache) are not the tree's
// and are covered by go.sum, which every hash includes.
func GoClosures(out, root string) map[string][]string {
	dirOf := map[string]string{}
	var recs [][]string
	for _, ln := range strings.Split(out, "\n") {
		// A package that imports nothing ends in a tab the caller's trim may
		// have taken, so a short record is padded: no deps rather than no
		// record. A line that is no record at all has no directory, and no
		// directory is under root.
		f := append(strings.SplitN(ln, "\t", 3), "", "")
		importPath, _, _ := strings.Cut(f[0], " ")
		dirOf[importPath] = f[1]
		recs = append(recs, f)
	}
	sets := map[string]map[string]bool{}
	for _, f := range recs {
		unit, ok := relTo(root, f[1])
		if !ok {
			continue
		}
		if sets[unit] == nil {
			sets[unit] = map[string]bool{unit: true}
		}
		// A test variant in Deps, `p [p.test]`, splits into `p` — the package
		// — and `[p.test]`, which names no directory and is passed over.
		for _, d := range strings.Fields(f[2]) {
			if dep, ok := relTo(root, dirOf[d]); ok {
				sets[unit][dep] = true
			}
		}
	}
	closures := map[string][]string{}
	for unit, set := range sets {
		closures[unit] = slices.Sorted(maps.Keys(set))
	}
	return closures
}

// UnitKeys answers the key of every unit that owns one of the changed files,
// in path order. closures maps a unit to its closure; a unit with none, or a
// tree that could not be read (failed non-empty), is keyed with Err set — it
// is graded and stored unkeyable, never skipped. The owner it returns maps a
// file to its unit for BuildGradings.
func UnitKeys(lang unitkey.Lang, entries []unitkey.Entry, diff string, closures map[string][]string, changed []string, failed string) ([]UnitKey, func(string) (string, bool)) {
	units := unitkey.Units(lang, entries)
	owner := func(file string) (string, bool) { return unitkey.Owner(lang, file, units) }
	ranges := unitkey.ParseRanges(diff)
	seen := map[string]bool{}
	var keys []UnitKey
	for _, file := range changed {
		unit, ok := owner(file)
		if !ok || seen[unit] {
			continue
		}
		seen[unit] = true
		k := UnitKey{Unit: unit, Closure: closures[unit], Err: failed}
		if k.Closure == nil && failed == "" {
			k.Err = "the toolchain named no closure for " + unit
		}
		if k.Err == "" {
			k.Hash = unitkey.Hash(lang, entries, k.Closure)
			k.Ranges = unitkey.Ranges(lang, entries, unit, ranges)
		}
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b UnitKey) int { return strings.Compare(a.Unit, b.Unit) })
	return keys, owner
}

// InModule maps module-relative paths onto the repository root.
func InModule(dir string, files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, path.Join(dir, f))
		}
	}
	return out
}

// ReadFailed names a key read that did not answer — the engine could not run
// it, or it exited non-zero — and is "" for one that did.
func ReadFailed(what string, code int, err error) string {
	if err != nil {
		return what + " never ran: " + err.Error()
	}
	if code != 0 {
		return fmt.Sprintf("%s exited %d", what, code)
	}
	return ""
}

// GoGradings are a Go mutation run's gradings: the score's mutants moved from
// the module onto the repository root, split by unit under the run's keys.
// A run with no score (no report) grades its keyed units as having made no
// mutant, trusted or not as the run was.
func GoGradings(score *GoMutationScore, dir, engine string, trusted bool, keys []UnitKey, owner func(string) (string, bool)) []Grading {
	var mutants []ScoredMutant
	if score != nil {
		for _, m := range score.Scored {
			m.File = path.Join(dir, m.File)
			mutants = append(mutants, m)
		}
	}
	return BuildGradings(string(unitkey.Go), engine, keys, owner, mutants, trusted)
}
