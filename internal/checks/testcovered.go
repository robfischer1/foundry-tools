package checks

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/unitkey"
)

// A TEST-ONLY CHANGE IS MUTATION-TESTED BY WHAT IT RUNS, in every language
// (gocovered.go says why). Each mutation atom finds the tests a change touched by
// its language's conventions, runs them alone under coverage, and hands the
// source lines they executed to its tool beside the diff. This file is the part
// the four atoms share: the bound and the line that says what joined.

// EnvMutationCoveredCap bounds the lines a run's changed tests may add to a
// mutation atom's scope; the rest are cut, said on the verdict, and never a
// failure.
const EnvMutationCoveredCap = "ATOM_MUTATION_COVERED_CAP"

// EnvMutationCoveredCapRust is rust:mutation's own bound on a test-only scope.
const EnvMutationCoveredCapRust = "ATOM_MUTATION_COVERED_CAP_RUST"

// DefaultMutationCoveredCap is the bound when the knob is unset: about a
// night's worth of one module's mutants at the lane's measured cost.
const DefaultMutationCoveredCap = 2000

// MutationCoveredCap reads EnvMutationCoveredCap; unset or unreadable is the
// default, and a value of 0 or less is no bound.
func MutationCoveredCap() int { return coveredCapOf(EnvMutationCoveredCap, DefaultMutationCoveredCap) }

// DefaultMutationCoveredCapRust is rust's bound when its knob is unset. Rust's
// test-only scope is the owning crate's whole source (no line coverage in its
// toolchain), far more mutants per line a test touches, and at the lane's
// measured ~44s a mutant two at a time, 2000 lines would overrun the 75-minute
// deadline; 500 is about a lane's worth.
const DefaultMutationCoveredCapRust = 500

// RustMutationCoveredCap reads EnvMutationCoveredCapRust as MutationCoveredCap
// reads its knob. The shared knob does not move it.
func RustMutationCoveredCap() int {
	return coveredCapOf(EnvMutationCoveredCapRust, DefaultMutationCoveredCapRust)
}

// coveredCapOf reads a bound off env; unset or unreadable is def.
func coveredCapOf(env string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(env)); err == nil {
		return n
	}
	return def
}

// CoveredScope bounds covered by MutationCoveredCap and answers what is kept and
// the verdict's line for it. whose says whose files they are ("they run", "of
// their crate(s)"); an empty covered is kept as nothing and said as zero.
func CoveredScope(probe, whose string, covered map[string][]unitkey.Range) (map[string][]unitkey.Range, string) {
	return CoveredScopeBy(probe, whose, covered, EnvMutationCoveredCap, MutationCoveredCap())
}

// CoveredScopeBy is CoveredScope under the bound limit, which the knob env sets.
func CoveredScopeBy(probe, whose string, covered map[string][]unitkey.Range, env string, limit int) (map[string][]unitkey.Range, string) {
	kept, cutLines, cutFiles := CapMutationRanges(covered, limit)
	say := fmt.Sprintf("%s: changed tests: %d source line(s) in %d file(s) %s joined the scope", probe, RangeLines(kept), len(kept), whose)
	if cutLines > 0 {
		say += fmt.Sprintf("; %d line(s) in %d file(s) cut at the %s bound (%s)", cutLines, len(cutFiles), env, strings.Join(cutFiles[:min(len(cutFiles), 5)], ", "))
	}
	return kept, say
}

// RangeLines counts the lines every range names.
func RangeLines(r map[string][]unitkey.Range) int {
	n := 0
	for _, rs := range r {
		for _, x := range rs {
			n += x.End - x.Start + 1
		}
	}
	return n
}

// LineRanges is a set of 1-indexed lines as merged ranges; a line below 1 is
// dropped.
func LineRanges(lines []int) []unitkey.Range {
	var rs []unitkey.Range
	for _, l := range lines {
		if l > 0 {
			rs = append(rs, unitkey.Range{Start: l, End: l})
		}
	}
	return mergeRanges(rs)
}

// WholeFile is a file's every line as one range; an empty file is none.
func WholeFile(src string) []unitkey.Range {
	n := strings.Count(src, "\n")
	if src != "" && !strings.HasSuffix(src, "\n") {
		n++
	}
	if n == 0 {
		return nil
	}
	return []unitkey.Range{{Start: 1, End: n}}
}

// KeepCovered is covered less every file keep refuses.
func KeepCovered(covered map[string][]unitkey.Range, keep func(string) bool) map[string][]unitkey.Range {
	out := map[string][]unitkey.Range{}
	for f, rs := range covered {
		if keep(f) {
			out[f] = slices.Clone(rs)
		}
	}
	return out
}
