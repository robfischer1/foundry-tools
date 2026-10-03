package main

import (
	"cmp"
	"context"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/unitkey"
)

// THE GO LANE'S GRADING KEYS (incremental mutation, M1). Each unit the pull's
// Go diff touches is keyed by its content hash over its test closure, its
// change ranges and the engine — internal/unitkey — so the run's per-unit
// gradings can be stored and reused where the same key meets again. Three
// reads, none of which can change the verdict: a read that fails keys the
// units unkeyable, and they are graded exactly as before.

// goMutationGoflags is the GOFLAGS every mutant's tests run under.
const goMutationGoflags = "-p=1 -count=1"

// goMutationEngine is E for the Go lane: every input that decides which
// mutants gomutants makes and how they are graded. TestTheGoEngineIsPinned
// holds its value, so an input that moves is a decision someone makes.
func goMutationEngine() string {
	return unitkey.Engine(map[string]string{
		"image":          checks.ImageGo,
		"gomutants":      checks.GomutantsModule,
		"mutation-gate":  checks.MutationGateModule,
		"disable":        goMutationDisable,
		"exclude":        goMutationExclude,
		"workers":        strconv.Itoa(goMutationWorkers),
		"goflags":        goMutationGoflags,
		"timeout-budget": strconv.FormatFloat(checks.GoMutationTimeoutBudget, 'f', -1, 64),
		"pgvector":       checks.ImagePgvector,
		"postgres":       checks.ImagePostgres,
		"redpanda":       checks.ImageRedpanda,
		"epoch":          checks.MutationGradingEpoch,
	})
}

// goGradingKeys reads the tree, the -U0 diff gomutants reads, and every
// package's test closure, and keys the units the module-relative changed
// files fall in.
func goGradingKeys(ctx context.Context, ctr *dagger.Container, dir, since, changed string, tags []string) ([]checks.UnitKey, func(string) (string, bool)) {
	tree, code, err := output(ctx, ctr.WithExec([]string{"git", "-C", "/src", "ls-tree", "-r", "-z", "--full-tree", "HEAD"}, anyExit))
	failed := checks.ReadFailed("git ls-tree", code, err)
	diff, code, err := output(ctx, ctr.WithExec([]string{"git", "-C", "/src", "diff", "--unified=0", "--no-color", "--no-ext-diff", since, "HEAD"}, anyExit))
	failed = cmp.Or(failed, checks.ReadFailed("git diff", code, err))
	list, code, err := output(ctx, ctr.WithExec(append(append([]string{"go", "list", "-e", "-deps", "-test", "-f", checks.GoListDepsFormat}, tags...), "./..."), anyExit))
	failed = cmp.Or(failed, checks.ReadFailed("go list", code, err))
	return checks.UnitKeys(unitkey.Go, checks.ParseLsTree(tree), diff, checks.GoClosures(list, "/src"),
		checks.InModule(dir, strings.Split(changed, "\n")), failed)
}

// The Rust lane's per-mutant budgets, as cargo-mutants is handed them.
const (
	rustBuildTimeout   = "900"
	rustMinTestTimeout = "60"
)

// rustMutationEngine is E for the Rust lane. TestTheRustEngineIsPinned holds
// its value.
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
		"epoch":                checks.MutationGradingEpoch,
	})
}

// rustGradingKeys keys the units the pull's critical-module diff touched:
// the tree's blobs, the diff cargo-mutants is handed (its `+` lines are the
// ranges), each member's path-dependency closure off the metadata already
// read, widened to the workspace when the tree's mutants config tests wider
// than a mutant's own crate.
func rustGradingKeys(ctx context.Context, ctr *dagger.Container, diff, files, metadata, config string) ([]checks.UnitKey, func(string) (string, bool)) {
	tree, code, err := output(ctx, ctr.WithExec([]string{"git", "-C", "/src", "ls-tree", "-r", "-z", "--full-tree", "HEAD"}, anyExit))
	failed := checks.ReadFailed("git ls-tree", code, err)
	closures, err := checks.RustClosures([]byte(metadata), "/src")
	if err != nil {
		failed = cmp.Or(failed, err.Error())
	}
	if checks.RustTestScopeWide(config) {
		closures = checks.Widened(closures)
	}
	return checks.UnitKeys(unitkey.Rust, checks.ParseLsTree(tree), diff, closures, strings.Split(files, "\x00"), failed)
}
