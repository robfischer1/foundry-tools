// Package unitkey names a mutation grading by what it depends on, so a grading
// made once can be reused wherever the same inputs meet again.
//
// THE KEY IS (unit, H, R, E). A unit is the smallest thing whose tests alone
// grade its mutants: a Go package directory, a Rust workspace member. H is the
// unit's content hash — every file of the tree EXCEPT documentation and the
// files owned by units outside the unit's test closure. R is the changed line
// ranges of the unit's own files. E is the engine: every tool pin, image and
// flag that decides how a mutant is generated and graded.
//
// WHY THE WHOLE CLOSURE AND NOT THE MUTATED FILE. A mutant's verdict depends on
// the tests that run against it and on everything those tests import.
// gomutants' own cache learned this the hard way (its v7 schema note: "a
// KILLED mutant whose kill depended on a constant next door stayed KILLED after
// that constant changed"), and its key still omits imported packages,
// testdata and go.sum. Here every one of those is inside H.
//
// FILES NO UNIT OWNS ARE IN EVERY UNIT'S HASH. The justfile, a migrations
// directory a test reads by relative path, go.mod and Cargo.lock: none is a
// unit's own, any of them can change an outcome, so an edit to one re-grades
// everything. That over-invalidates on purpose — a miss only re-grades, a
// false hit reuses a verdict about code that is not there.
//
// TWO READERS, ONE RULE. The mutation lane (this module) computes these keys
// when it grades, and the door recomputes H at a pushed head from the stored
// closure to say which stored grades still describe it. Both must agree to the
// bit.
//
// THIS IS A COPY OF git.notusmi.com/rob/stellar-core-go/unitkey, and the copy
// is deliberate: this dagger module's dependencies are fetched by the engine's
// Go SDK build, which has no route to git.notusmi.com modules (GOPRIVATE is
// set nowhere on that path; every lane in the fleet loads this module at main,
// so a dependency that failed to resolve would fail every lane at once). The
// code below the doc comments is the same as stellar-core-go's, and
// testdata/vectors.json is byte-identical to its: TestTheGoldenVectors in each
// repository proves its copy answers every vector. Change both together.
package unitkey

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
)

// Version opens every hash. A change to any rule in this package bumps it, so
// no key made under the old rule can equal one made under the new.
const Version = "unitkey/1"

// Lang is a mutation lane's language: it decides what a unit is.
type Lang string

// The languages whose units this package knows.
const (
	Go   Lang = "go"
	Rust Lang = "rust"
)

// Entry is one file of a tree as `git ls-tree -r` names it: its repo-relative
// path and its blob sha. Contents are never read — the blob sha is the content.
type Entry struct {
	Path string `json:"path"`
	Blob string `json:"blob"`
}

// IsDoc reports documentation, which no test reads and no hash includes:
// Markdown anywhere, the root docs/ tree, and LICENSE files.
func IsDoc(p string) bool {
	return strings.HasSuffix(p, ".md") || strings.HasPrefix(p, "docs/") || strings.HasPrefix(path.Base(p), "LICENSE")
}

// Units answers the unit directories a tree holds, "." for the root.
//
// Go: every directory directly holding a .go file the go tool would build —
// not under testdata, vendor, or a directory starting with "." or "_", which
// the go tool ignores.
//
// Rust: every directory holding a Cargo.toml and a src/ beside it. A virtual
// workspace root has a manifest and no src/, so it is not a unit.
func Units(lang Lang, entries []Entry) map[string]bool {
	units := map[string]bool{}
	manifests := map[string]bool{}
	for _, e := range entries {
		if path.Base(e.Path) == "Cargo.toml" {
			manifests[path.Dir(e.Path)] = true
		}
	}
	for _, e := range entries {
		switch lang {
		case Go:
			if strings.HasSuffix(e.Path, ".go") && !ignoredByGo(e.Path) {
				units[path.Dir(e.Path)] = true
			}
		case Rust:
			parts := strings.Split(e.Path, "/")
			for i := range len(parts) - 1 {
				if dir := joinDir(parts[:i]); parts[i] == "src" && manifests[dir] {
					units[dir] = true
				}
			}
		}
	}
	return units
}

// ignoredByGo reports a path the go tool does not build: one with a directory
// named testdata or vendor, or starting with "." or "_".
func ignoredByGo(p string) bool {
	parts := strings.Split(p, "/")
	for _, d := range parts[:len(parts)-1] {
		if d == "testdata" || d == "vendor" || strings.HasPrefix(d, "_") || strings.HasPrefix(d, ".") {
			return true
		}
	}
	return false
}

// joinDir is a directory from its path components, "." for none.
func joinDir(parts []string) string {
	if len(parts) == 0 {
		return "."
	}
	return strings.Join(parts, "/")
}

// forcedOrphan reports the build-wide files no unit may own, whatever
// directory they sit in: Go's module and workspace files, and a Cargo.lock.
// The rest of a Rust repository's build-wide files — the root Cargo.toml,
// .cargo/ (where cargo-mutants reads mutants.toml), rust-toolchain — sit at
// the root, where rustRootOwned already leaves them to nobody. Any language
// but Go reads Rust's rule; Owner gives such a language no unit.
func forcedOrphan(lang Lang, p string) bool {
	base := path.Base(p)
	if lang == Go {
		return base == "go.mod" || base == "go.sum" || base == "go.work" || base == "go.work.sum"
	}
	return base == "Cargo.lock"
}

// rustRootOwned reports the files a crate at the repository root owns: its
// sources, tests, benches, examples and build script. Everything else at the
// root is the repository's, not the crate's, and so is an orphan.
func rustRootOwned(p string) bool {
	first, _, nested := strings.Cut(p, "/")
	return p == "build.rs" || (nested && slices.Contains([]string{"src", "tests", "benches", "examples"}, first))
}

// Owner answers the unit that owns a path, or false for an orphan.
//
// Go: the package directory the file sits in directly, or — under testdata —
// the package directory above the first testdata. A file in a directory that
// is no package (migrations/, deploy/) is an orphan.
//
// Rust: the nearest ancestor unit; at the root only rustRootOwned files.
func Owner(lang Lang, p string, units map[string]bool) (string, bool) {
	if forcedOrphan(lang, p) {
		return "", false
	}
	switch lang {
	case Go:
		dir := path.Dir(p)
		if before, _, found := strings.Cut("/"+dir+"/", "/testdata/"); found {
			dir = path.Clean("." + before)
		}
		if units[dir] {
			return dir, true
		}
	case Rust:
		parts := strings.Split(p, "/")
		for k := range len(parts) {
			d := joinDir(parts[:len(parts)-1-k])
			if units[d] {
				if d != "." || rustRootOwned(p) {
					return d, true
				}
				return "", false
			}
		}
	}
	return "", false
}

// Hash is H: sha256 over the sorted (path, blob) pairs of every file that is
// not documentation and is either an orphan or owned by a unit in closure.
// closure holds unit directories ("." for the root), in any order.
func Hash(lang Lang, entries []Entry, closure []string) string {
	units := Units(lang, entries)
	in := map[string]bool{}
	for _, c := range closure {
		in[path.Clean(c)] = true
	}
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
	h := sha256.New()
	fmt.Fprintf(h, "%s %s\n", Version, lang)
	for _, e := range sorted {
		if IsDoc(e.Path) {
			continue
		}
		if own, ok := Owner(lang, e.Path, units); ok && !in[own] {
			continue
		}
		fmt.Fprintf(h, "%s\x00%s\n", e.Path, e.Blob)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Engine is E: sha256 over the sorted name=value inputs that decide how a
// mutant is generated and graded.
func Engine(inputs map[string]string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s engine\n", Version)
	for _, n := range slices.Sorted(maps.Keys(inputs)) {
		fmt.Fprintf(h, "%s=%s\n", n, inputs[n])
	}
	return hex.EncodeToString(h.Sum(nil))
}
