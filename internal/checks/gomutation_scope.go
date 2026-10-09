package checks

import (
	"encoding/json"
	"strings"
)

// WHAT gomutants IS HANDED: THE PACKAGES ITS MUTANTS CAN BE IN, NOT ./... .
//
// Every phase gomutants runs before the first mutant — `go test -coverprofile`,
// the baseline, and the per-test coverage map, which runs EVERY TEST of every
// package it is given one at a time — runs over the package patterns on its
// argv. Handed ./..., a pull that touched one package paid for the whole
// module's suite three times over before a single mutant ran: the fixed ~61
// CPU-s a run measured (tesla38-011), p90 52 s of baseline alone, 110-160 s on
// ourea.
//
// SCOPING CANNOT LOSE A KILL, and that is read off the pinned source, not
// assumed (gomutants v0.6.1). Without --integration or --coverpkg, each
// package's test binary records coverage of ITS OWN package only
// (coverage.BuildTestMap compiles each package's tests with no -coverpkg), so
// the per-test map can only ever route a mutant to tests in the mutant's own
// package, and runner.testInvocations runs exactly those — or, with no map, the
// mutant's own package whole. A test in another package never ran against a
// mutant under ./... either. The mutant population is the same too: it is
// filtered to the -changed-since lines, which are all in these packages.
//
// WHAT DOES MOVE is the baseline's ceiling: baseline×coefficient is measured
// over the packages handed, so it shrinks to the changed packages' own suites.
// A mutant runs only tests of its own package, which that baseline includes,
// and the adaptive per-test deadline is unchanged — so the cap shrinks to what
// a mutant's run can actually need, times ten.

// GoSourcePackagesFormat is the `go list -e -f` template naming every package
// of a module that holds a buildable non-test Go file — the only files
// gomutants mutates — by directory, one per line. A test-only package, a
// directory a build constraint empties and a nested module's directory are
// never named, so none of them can reach the argv as a pattern `go test`
// refuses.
const GoSourcePackagesFormat = `{{if or .GoFiles .CgoFiles}}{{.Dir}}{{"\n"}}{{end}}`

// GoMutationScope is the package patterns gomutants is handed for a module at
// dir: the units it grades (misses — every keyed unit, or the ones no reuse
// answered) that `go list` named as packages with source (listed, absolute
// directories, from GoSourcePackagesFormat; root is the repository's absolute
// directory, which units are relative to). A unit
// that is no such package has nothing to mutate and is left out.
//
// NOTHING LEFT IS ./..., exactly what every run was handed before this: a
// listing that failed, or a diff whose Go is all testdata, is graded the way
// it always was rather than over an empty argv, which `go test` reads as "the
// current directory".
func GoMutationScope(dir string, misses []UnitKey, listed, root string) []string {
	have := map[string]bool{}
	for _, ln := range strings.Split(listed, "\n") {
		if unit, ok := relTo(root, strings.TrimSpace(ln)); ok {
			have[unit] = true
		}
	}
	var keep []UnitKey
	for _, k := range misses {
		if have[k.Unit] {
			keep = append(keep, k)
		}
	}
	if len(keep) == 0 {
		return []string{"./..."}
	}
	return MissPatterns(dir, keep)
}

// GoCachedProfile is the coverage profile gomutants measured, read off the
// cache file it wrote (its `coverage_profile`, schema v3+). "" for a file that
// is absent, unparseable or carries none — the profile only corrects a
// misjudged NOT COVERED, and its absence is the scorer's ordinary case.
//
// THE CACHE FILE IS THE ONE PLACE gomutants LEAVES ITS PROFILE: it collects it
// under a temp directory it removes on exit. Reading it here is what lets the
// lane drop its own `go test -coverprofile` over the same packages — the same
// command (`go test -count=1 -coverprofile`, mode set, the run's tags) run a
// second time for nothing.
func GoCachedProfile(raw string) string {
	var c struct {
		CoverageProfile string `json:"coverage_profile"`
	}
	// NO BRANCH ON THE ERROR: Unmarshal validates the whole input before it
	// sets a field, so a file that is not JSON leaves c empty, and a field of
	// the wrong type is skipped — "" either way, which is the answer.
	_ = json.Unmarshal([]byte(raw), &c)
	return c.CoverageProfile
}
