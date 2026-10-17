package gomut

import (
	"cmp"
	"context"
	"errors"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/unitkey"
)

// THE GO LANE'S GRADING KEYS (incremental mutation, M1). Each unit the pull's Go
// diff touches is keyed by its content hash over its test closure, its change
// ranges and the engine — internal/unitkey — so the run's per-unit gradings can
// be stored and reused where the same key meets again. Three reads, none of
// which can change the verdict: a read that fails keys the units unkeyable, and
// they are graded exactly as before.

// goMutationGoflags is the GOFLAGS every mutant's tests run under.
const goMutationGoflags = "-p=1 -count=1"

// goMutationEngine is E for the Go lane: every input that decides which mutants
// gomutants makes and how they are graded. TestTheGoEngineIsPinned holds its
// value, so an input that moves is a decision someone makes.
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

// readFailed words a key read that did not answer: a program that would not
// start never ran, one that exited non-zero exited.
func readFailed(what, out string, code int) string {
	if code < 0 {
		return checks.ReadFailed(what, 0, errors.New(out))
	}
	return checks.ReadFailed(what, code, nil)
}

// gradingKeys reads the tree, the -U0 diff gomutants reads, and every package's
// test closure, and keys the units the module-relative changed files fall in.
func (m *module) gradingKeys(ctx context.Context, since, changed string, tags []string) ([]checks.UnitKey, func(string) (string, bool)) {
	root := m.in.Root
	tree, code := m.run(ctx, atoms.Cmd{Dir: m.cwd, Name: "git", Args: []string{"-C", root, "ls-tree", "-r", "-z", "--full-tree", "HEAD"}})
	failed := readFailed("git ls-tree", tree, code)
	diff, code := m.run(ctx, atoms.Cmd{Dir: m.cwd, Name: "git", Args: []string{"-C", root, "diff", "--unified=0", "--no-color", "--no-ext-diff", since, "HEAD"}})
	failed = cmp.Or(failed, readFailed("git diff", diff, code))
	args := append(append([]string{"list", "-e", "-deps", "-test", "-f", checks.GoListDepsFormat}, tags...), "./...")
	list, code := m.run(ctx, atoms.Cmd{Dir: m.cwd, Name: "go", Args: args})
	failed = cmp.Or(failed, readFailed("go list", list, code))
	return checks.UnitKeys(unitkey.Go, checks.ParseLsTree(tree), diff, checks.GoClosures(list, root),
		checks.InModule(m.dir, strings.Split(changed, "\n")), failed)
}
