package pymut

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// uvSync installs the consumer's suite into dir's own environment. SYNC only now
// there is work: a stale uv.lock on a pull that touched no critical module is not
// this gate's to red.
func (g *grading) uvSync(ctx context.Context, dir string) (checks.Verdict, bool) {
	s := g.do(ctx, dir, nil, "uv", "sync", "--all-extras", "--locked")
	if !s.ran() {
		return g.neverRan(s), false
	}
	if s.code != 0 {
		return g.settle(2, fmt.Sprintf("CANNOT RUN - uv sync --all-extras --locked exited %d — the consumer's suite could not be installed, so nothing was measured\n%s", s.code, lastLines(s.out, 20))), false
	}
	return checks.Verdict{}, true
}

// concurrently runs f for 0..n-1 at once and answers the results in order.
func concurrently[T any](n int, f func(i int) T) []T {
	out := make([]T, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { out[i] = f(i) })
	}
	wg.Wait()
	return out
}

// measure is the gate once there is something to mutate: the suite installed,
// the session initialised, scoped and planned, then measured and executed by the
// workers and scored.
func (g *grading) measure(ctx context.Context, since, diff string, kept []string) checks.Verdict {
	g.tree = filepath.Join(g.work, "src")
	if err := removeAll(g.work); err != nil {
		return g.settle(2, "CANNOT RUN - the atom never ran: "+err.Error())
	}
	if err := copyTree(g.in.Root, g.tree, ".venv"); err != nil {
		return g.settle(2, "CANNOT RUN - the atom never ran: the tree could not be copied to work in: "+err.Error())
	}
	if v, ok := g.uvSync(ctx, g.tree); !ok {
		return v
	}
	diffFile := filepath.Join(g.work, "pr.diff")
	cfg := filepath.Join(g.tree, pythonMutationConfig)
	if err := writeAll(map[string]string{
		cfg:      checks.CosmicRayConfig(kept, checks.PythonTestCommand, checks.PythonMutationTimeout),
		diffFile: diff + "\n",
	}); err != nil {
		return g.settle(2, "CANNOT RUN - the atom never ran: "+err.Error())
	}
	inited := g.do(ctx, g.tree, nil, "uv", "run", "cosmic-ray", "init", pythonMutationConfig, session)
	if !inited.ran() {
		return g.neverRan(inited)
	}
	if inited.code != 0 {
		return g.cannot("cosmic-ray init enumerated no mutation sites", inited)
	}

	// SCOPE: drop the out-of-diff jobs. The base rides along (testkit 1.8.0), so an
	// added line inside a unit whose AST is unchanged — a reformat — leaves the
	// scope too. So do the fleet's skipped operator classes (checks.RatifiedMutators,
	// testkit 2.1.0): deleted here rather than filtered, because a filtered job
	// reports as "unknown" on every run.
	scopeArgs := []string{"scope", session, "--diff", diffFile, "--base", since}
	for _, op := range checks.SkippedMutators(checks.MutatorPython) {
		scopeArgs = append(scopeArgs, "--exclude-operator", op)
	}
	stepOut := filepath.Join(g.work, "scope.out")
	if err := writeFile(stepOut, nil, 0o644); err != nil {
		return g.settle(2, "CANNOT RUN - the atom never ran: "+err.Error())
	}
	scoped := g.testkit(ctx, []string{"GITHUB_OUTPUT=" + stepOut}, scopeArgs...)
	if !scoped.ran() {
		return g.neverRan(scoped)
	}
	if scoped.code != 0 {
		// The diff's paths match no enumerated site: a green over zero mutants is
		// the failure this gate exists to close, and the declaration is the star's
		// to fix.
		return g.settle(1, fmt.Sprintf("forge-testkit-mutation scope exited %d — the diff's paths match no enumerated mutation site; check critical-modules against the tree\n%s", scoped.code, lastLines(scoped.out, 20)))
	}
	written, _ := readFile(stepOut)
	if checks.PythonUnmutable(string(written)) {
		return g.settle(0, "no mutable line in the diff — zero mutants ran")
	}

	plugin := filepath.Join(g.work, "plugin")
	plugged := g.testkit(ctx, nil, "plugin", "--out", plugin)
	if !plugged.ran() {
		return g.neverRan(plugged)
	}
	if plugged.code != 0 {
		return g.cannot("the test-selection plugin could not be written", plugged)
	}
	planned := g.testkit(ctx, nil, "plan", session, "--workers", strconv.Itoa(checks.PythonMutationWorkers))
	if !planned.ran() {
		return g.neverRan(planned)
	}
	workers, _, ok := checks.PythonPlan(planned.out)
	if planned.code != 0 || !ok {
		return g.cannot("the session could not be planned", planned)
	}
	return g.fanOut(ctx, workers, kept, plugin)
}

// writeAll writes each file, making its directories.
func writeAll(files map[string]string) error {
	for p, body := range files {
		if err := mkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := writeFile(p, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// worker is one copy of the tree and its own environment.
func (g *grading) worker(i int) string { return filepath.Join(g.work, "w"+strconv.Itoa(i+1)) }

// fanOut measures and executes on the workers and scores the merged session.
func (g *grading) fanOut(ctx context.Context, workers int, kept []string, plugin string) checks.Verdict {
	// Each worker is a copy of the planned primary — its session and config come
	// with it — synced its own environment: a copied .venv would import the
	// primary's source, because an editable install names its tree absolutely.
	copies := concurrently(workers, func(i int) checks.Verdict {
		if err := copyTree(g.tree, g.worker(i), ".venv"); err != nil {
			return g.settle(2, "CANNOT RUN - the atom never ran: the tree could not be copied for a worker: "+err.Error())
		}
		v, ok := g.uvSync(ctx, g.worker(i))
		if ok {
			return checks.Verdict{}
		}
		return v
	})
	for _, v := range copies {
		if v.Atom != "" {
			return v
		}
	}

	// MEASURE, once per worker and concurrently, so the elapsed time carries the
	// contention the mutants will. FORGE_MUT_WORKER tells the runs apart.
	pluginEnv := []string{"PYTHONPATH=" + plugin, "PYTEST_ADDOPTS=-p _forge_mutation_select"}
	measureDir := filepath.Join(g.work, "measure")
	include := strings.Join(kept, " ")
	started := time.Now()
	measured := concurrently(workers, func(i int) step {
		env := append(slicesClone(pluginEnv), "FORGE_MUT_WORKER="+strconv.Itoa(i+1), "PYTHONDONTWRITEBYTECODE=1",
			"FORGE_MUT_MEASURE="+measureDir, "FORGE_MUT_INCLUDE="+include)
		return g.do(ctx, g.worker(i), env, "uv", append([]string{"run", "--with", checks.PythonWith("coverage")}, checks.PythonTestCommand...)...)
	})
	elapsed := int(time.Since(started).Seconds())
	for i, m := range measured {
		if !m.ran() {
			return g.neverRan(m)
		}
		if why := checks.PythonMeasureFailure(i+1, m.code, m.out); why != "" {
			return g.settle(2, why)
		}
	}
	mapFile := filepath.Join(g.work, "map.json")
	mapped := g.testkit(ctx, pluginEnv, "map", "--parts", measureDir, "--modules", include, "--root", g.tree, "--out", mapFile)
	if !mapped.ran() {
		return g.neverRan(mapped)
	}
	if mapped.code != 0 {
		return g.cannot("the test map could not be built", mapped)
	}

	// EXEC, on the session partitioned across the workers.
	selectLog := filepath.Join(g.work, "select.log")
	selectEnv := append(slicesClone(pluginEnv), "FORGE_MUT_SELECT="+mapFile, "FORGE_MUT_SELECT_LOG="+selectLog)
	config := checks.CosmicRayConfig(kept, checks.PythonTestCommand, checks.PythonMutantTimeout(elapsed))
	files := map[string]string{filepath.Join(g.tree, pythonMutationConfig): config}
	for i := range workers {
		files[filepath.Join(g.worker(i), pythonMutationConfig)] = config
	}
	if err := writeAll(files); err != nil {
		return g.settle(2, "CANNOT RUN - the atom never ran: "+err.Error())
	}
	// Each worker's session is named once, where partition writes it and the
	// worker reads it.
	partition := []string{"partition", session, "--map", mapFile}
	sessions := make([]string, workers)
	for i := range workers {
		sessions[i] = filepath.Join(g.work, fmt.Sprintf("w%d.sqlite", i+1))
		partition = append(partition, "--out", sessions[i])
	}
	partitioned := g.testkit(ctx, selectEnv, partition...)
	if !partitioned.ran() {
		return g.neverRan(partitioned)
	}
	if partitioned.code != 0 {
		return g.cannot(fmt.Sprintf("the session could not be partitioned across %d workers", workers), partitioned)
	}
	return g.execute(ctx, workers, sessions, selectEnv, selectLog)
}

func slicesClone(s []string) []string { return append([]string(nil), s...) }
