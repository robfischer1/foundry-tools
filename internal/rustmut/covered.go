package rustmut

import (
	"context"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/unitkey"
)

// A CHANGED TEST IS GRADED BY ITS CRATE (checks/testcovered.go says why a test
// change is graded at all). A test change is a rust file under a crate's tests/
// or benches/, or a changed line inside a #[cfg(test)] item of a source file.
//
// BY CRATE, NOT BY LINE: the lane's toolchain (rust-trixie, upstream's
// rust:1.97.0-trixie) carries neither cargo-llvm-cov nor the llvm-tools
// component, so there is no line coverage to read; the tools.toml pin set has
// none either. The owning crate's source files join the scope whole, under
// rust's own lower bound (ATOM_MUTATION_COVERED_CAP_RUST), and the verdict says the
// scope is the crate's. cargo-mutants never mutates the tests themselves: tests/
// is no source file here, and it skips #[cfg(test)] items in the ones that are.

// testCrates answers the source lines of every crate a test change belongs to,
// named from the root, and the verdict's line; nil and "" when no test changed.
func (g *grading) testCrates(ctx context.Context, since string) (map[string][]unitkey.Range, string) {
	c := atoms.Cmd{Dir: g.in.Root, Name: "git", Args: []string{"diff", "--relative", "--unified=0", since, "HEAD", "--", "*.rs"}}
	diff, code := atoms.RunCmd(ctx, g.in, c)
	if code != 0 {
		return nil, id + ": changed tests: git could not diff them, so their crates joined no scope: " + atoms.LastLine(diff)
	}
	crates := map[string]bool{}
	for f, rs := range unitkey.ParseRanges(diff) {
		if !checks.RustTestFile(f) && !inTestItem(g.in.Root, f, rs) {
			continue
		}
		if crate, ok := crateOf(g.in.Root, f); ok {
			crates[crate] = true
		}
	}
	if len(crates) == 0 {
		return nil, ""
	}
	if g.in.FilesErr != nil {
		return nil, id + ": changed tests: the tree could not be listed, so their crates joined no scope"
	}
	mods := strings.Fields(g.mods)
	covered := map[string][]unitkey.Range{}
	for _, f := range g.in.Files {
		if !strings.HasSuffix(f, ".rs") || checks.RustTestFile(f) || (len(mods) > 0 && !checks.UnderAny(f, mods)) {
			continue
		}
		crate, ok := crateOf(g.in.Root, f)
		if !ok || !crates[crate] || !checks.UnderAny(f, []string{path.Join(crate, "src")}) {
			continue
		}
		src, _ := atoms.ReadIfPresent(g.in.Root, f)
		if rs := checks.WholeFile(src); rs != nil {
			covered[f] = rs
		}
	}
	// RUST'S OWN BOUND (checks.RustMutationCoveredCap): a crate is far more
	// mutants than the lines a test runs.
	kept, say := checks.CoveredScopeBy(id, "of their crate(s)", covered, checks.EnvMutationCoveredCapRust, checks.RustMutationCoveredCap())
	return kept, say + " (no line coverage in this toolchain: cargo-llvm-cov is not in the lane's image, so the crate is the scope: " +
		strings.Join(slices.Sorted(maps.Keys(crates)), ", ") + ")"
}

// inTestItem says a changed line of a source file sits in a #[cfg(test)] item.
func inTestItem(root, f string, changed []unitkey.Range) bool {
	src, _ := atoms.ReadIfPresent(root, f) // deleted or unreadable: no test item
	for _, span := range checks.RustTestModSpans(src) {
		for _, r := range changed {
			if r.Start <= span.End && span.Start <= r.End {
				return true
			}
		}
	}
	return false
}

// crateOf is the directory of the package manifest nearest above f, "." for the
// root; a workspace manifest with no [package] owns no file.
func crateOf(root, f string) (string, bool) {
	for d := path.Dir(f); ; d = path.Dir(d) {
		if m, ok := atoms.ReadIfPresent(root, filepath.ToSlash(path.Join(d, "Cargo.toml"))); ok && strings.Contains(m, "[package]") {
			return d, true
		}
		if d == "." || d == "/" {
			return "", false
		}
	}
}
