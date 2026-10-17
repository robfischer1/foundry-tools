package rustmut

import (
	"cmp"
	"context"
	"errors"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/unitkey"
)

// The Rust lane's per-mutant budgets, as cargo-mutants is handed them.
const (
	rustBuildTimeout   = "900"
	rustMinTestTimeout = "60"
)

// rustMutationEngine is E for the Rust lane: every input that decides which
// mutants cargo-mutants makes and how they are graded. TestTheRustEngineIsPinned
// holds its value.
func rustMutationEngine() string {
	return unitkey.Engine(map[string]string{
		"image":                checks.ImageRust,
		"cargo-mutants":        checks.CargoMutantsVersion,
		"cargo-nextest":        checks.CargoNextestVersion,
		"python":               checks.FleetPython,
		"jobs":                 strconv.Itoa(rustMutationJobs),
		"build-timeout":        rustBuildTimeout,
		"minimum-test-timeout": rustMinTestTimeout,
		"test-runner":          rustTestRunner,
		"exclude-re":           strings.Join(checks.SkippedMutators(checks.MutatorRust), "\n"),
		"epoch":                checks.MutationGradingEpoch,
	})
}

// gradingKeys keys the units the pull's critical-module diff touched: the tree's
// blobs, the diff cargo-mutants is handed (its `+` lines are the ranges), each
// member's path-dependency closure off the metadata already read, widened to the
// workspace when the tree's mutants config tests wider than a mutant's own crate.
func (g *grading) gradingKeys(ctx context.Context, diff, files, metadata, config string) ([]checks.UnitKey, func(string) (string, bool)) {
	tree, code := g.run(ctx, g.in.Root, "git", "-C", g.in.Root, "ls-tree", "-r", "-z", "--full-tree", "HEAD")
	failed := readFailed("git ls-tree", tree, code)
	closures, err := checks.RustClosures([]byte(metadata), g.tree)
	if err != nil {
		failed = cmp.Or(failed, err.Error())
	}
	if checks.RustTestScopeWide(config) {
		closures = checks.Widened(closures)
	}
	return checks.UnitKeys(unitkey.Rust, checks.ParseLsTree(tree), diff, closures, strings.Split(files, "\x00"), failed)
}

// readFailed words a key read that did not answer.
func readFailed(what, out string, code int) string {
	if code < 0 {
		return checks.ReadFailed(what, 0, errors.New(out))
	}
	return checks.ReadFailed(what, code, nil)
}
