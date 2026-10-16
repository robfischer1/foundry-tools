package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE PYTHON LANE'S CHAINS. What is left here is the mutation gate: it fans a
// worker out per container filesystem, which one atom's exec cannot express, so
// it stays a chain. The lane's other atoms live in atoms/python/<name>/.
//
// pyright is deliberately absent, not overlooked. Two type checkers ran on every
// python pre-push with no incident behind the duplication, and it is dropped
// fleet-wide (decided, Rob). mypy is the one.

func init() {
	register("python:mutation", pythonMutation)
}

// pythonFiles is the tree's own python (checks.PythonFiles), read once per run
// off the gate's own population — the same shape as goModuleDirs, because the
// two lanes now answer the same question about themselves.
func (r *run) pythonFiles(ctx context.Context) ([]string, error) {
	r.pyFilesOnce.Do(func() {
		files, err := r.population(ctx, "**/*.py")
		if err != nil {
			r.pyFilesErr = err
			return
		}
		r.pyFiles = checks.PythonFiles(files)
	})
	return r.pyFiles, r.pyFilesErr
}

// Every mutant cosmic-ray makes of this pull's changes to the declared
// critical modules is killed by the tests.
//
// THE MEASUREMENT IS PLAIN EXECS, SETTLED IN GO. foundry-stocks'
// ci/lib/mutation/python.sh ran here as seven bash phases; git, uv, cosmic-ray
// and forge-testkit-mutation now run as their own execs, and
// checks.PythonReportVerdict reads the report.
//
// A WORKER IS A BRANCH OF THE CHAIN. cosmic-ray writes each mutant onto the
// disk, so two workers cannot share a tree, and a worker whose editable
// install pointed at another tree would import the unmutated source and every
// mutant it ran would survive. python.sh tarred the checkout into one copy per
// worker and synced each its own environment. Here each worker is a branch of
// the one synced container: its own filesystem, at the same /src its
// environment was installed against, run concurrently by the engine.
//
// EACH MUTANT RUNS ITS OWN TESTS (forge-testkit 1.9.0). The unmutated suite
// runs once per worker under coverage with the selection plugin — the MEASURE,
// and the baseline: a suite that fails unmutated would score every mutant
// killed. The parts become a map from each scoped line to the tests that can
// observe it, the session is partitioned across the workers, and each worker
// runs only the tests its mutant can reach.
//
// THE GATE MUST NOT INHERIT THE CONSUMER'S LOCK. Every forge-testkit-mutation
// call is `uv run --no-project --isolated --index <fleet> --with <floor>`:
// --no-project detaches the resolution from the project, the floor selects a
// testkit that has the CLI (eros#112 resolved 0.3.1 without it), --isolated
// keeps the star's synced .venv out (forge-testkit#3 ran its own tree), and
// --index names the one index that serves it (forge-testkit#3 at 002c25e).
// Do not tidy any of the four away.
func pythonMutation(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("python:mutation")
	// Absent answers file, absent declaration: "" is the whole diff.
	answers, _, _ := fileIfPresent(ctx, r.src, ".copier-answers.yml")
	mods := checks.CriticalModules(answers)
	scope := checks.MutationScope(a.ID, mods)
	settle := func(state int, reason string) checks.Verdict {
		v := checks.VerdictOf(a, state, a.ID+": "+reason)
		v.Reason = scope + "\n" + v.Reason
		return v
	}
	neverRan := func(err error) checks.Verdict { return settle(2, "CANNOT RUN - the atom never ran: "+err.Error()) }
	// ran runs one exec and answers its combined output and exit, or settles
	// could-not-run when the engine did not run it at all.
	type ran struct {
		ctr  *dagger.Container
		out  string
		code int
	}
	do := func(ctr *dagger.Container, args ...string) (ran, error) {
		next := ctr.WithExec(args, anyExit)
		out, code, err := outputBoth(ctx, next)
		return ran{next, out, code}, err
	}

	const noBase = "no usable PR base sha — the diff-scoped mutation gate did not run"
	if r.base == "" {
		return settle(0, noBase)
	}
	ctr := r.gitReady(ctx, r.withBase(r.lane(checks.ImagePython))).
		// Provisioning, under the default Expect: an image without uv or
		// prlimit is a Dagger error, and the first exec read below files it as
		// never ran.
		WithExec([]string{"uv", "--version"}).
		WithExec([]string{"prlimit", "--version"})

	// The change set starts at the merge base, not at the base the door named
	// (run.changeBase): main's tip moves under an open pull.
	since, err := r.changeBase(ctx, ctr)
	if err != nil {
		return settle(2, "CANNOT RUN - "+err.Error())
	}
	if since == "" {
		return settle(r.missingBase(noBase))
	}

	// RESOLVE: the declared modules, or every python source the pull added or
	// changed outside the tests; less the generated ones.
	modules := strings.Fields(mods)
	if len(modules) == 0 {
		out, code, err := output(ctx, ctr.WithExec(append([]string{"git", "diff", "--name-only", "--diff-filter=AM", since, "HEAD", "--"}, checks.PythonWholeDiffSpecs...), anyExit))
		if err != nil {
			return neverRan(err)
		}
		if code != 0 {
			return settle(2, "CANNOT RUN - git could not list the python the pull changed: "+out)
		}
		modules = strings.Fields(out)
	}
	var kept []string
	for _, m := range modules {
		// A directory or an absent path does not read, and is kept.
		if src, err := r.src.File(m).Contents(ctx); err != nil || !checks.PythonGenerated(src) {
			kept = append(kept, m)
		}
	}
	if len(kept) == 0 {
		return settle(0, "no hand-written python in scope — nothing to mutate")
	}
	// The whole tree, with renames paired, narrowed to the kept modules in Go:
	// a pathspec of the kept modules alone cannot see a file's old path.
	whole, code, err := output(ctx, ctr.WithExec([]string{"git", "diff", "-M", "--unified=0", since, "HEAD"}, anyExit))
	if err != nil {
		return neverRan(err)
	}
	if code != 0 {
		return settle(2, "CANNOT RUN - git could not diff the pull against its base "+since+": "+whole)
	}
	diff := checks.PythonDiffFor(whole, kept)
	// A PURE-DELETION PULL touches the module and adds no mutable line, and
	// scope leaves the session intact for it: exec would run every site init
	// enumerated (oceanus: a two-line deletion became a 43-minute gate).
	if !checks.DiffAddsLines(diff) {
		return settle(0, "this pull added no line to the critical modules — nothing to mutate")
	}

	// SYNC only now there is work: a stale uv.lock on a pull that touched no
	// critical module is not this gate's to red.
	synced, err := do(ctr, "uv", "sync", "--all-extras", "--locked")
	if err != nil {
		return neverRan(err)
	}
	if synced.code != 0 {
		return settle(2, fmt.Sprintf("CANNOT RUN - uv sync --all-extras --locked exited %d — the consumer's suite could not be installed, so nothing was measured\n%s", synced.code, lastLines(synced.out, 20)))
	}

	tk := func(ctr *dagger.Container, args ...string) (ran, error) {
		return do(ctr, append([]string{"uv", "run", "--no-project", "--isolated",
			"--with", checks.PythonMutationTestkit, "forge-testkit-mutation"}, args...)...)
	}
	cannot := func(what string, step ran) checks.Verdict {
		return settle(2, fmt.Sprintf("CANNOT RUN - %s (exit %d)\n%s", what, step.code, lastLines(step.out, 20)))
	}

	inited, err := do(synced.ctr.
		WithNewFile(pythonMutationConfig, checks.CosmicRayConfig(kept, checks.PythonTestCommand, checks.PythonMutationTimeout)).
		WithNewFile(pythonMutationDiff, diff+"\n"),
		"uv", "run", "cosmic-ray", "init", pythonMutationConfig, "session.sqlite")
	if err != nil {
		return neverRan(err)
	}
	if inited.code != 0 {
		return cannot("cosmic-ray init enumerated no mutation sites", inited)
	}

	// SCOPE: drop the out-of-diff jobs. The base rides along (testkit 1.8.0),
	// so an added line inside a unit whose AST is unchanged — a reformat —
	// leaves the scope too. So do the fleet's skipped operator classes
	// (checks.RatifiedMutators, testkit 2.1.0): deleted here rather than
	// filtered, because a filtered job reports as "unknown" on every run.
	scopeArgs := []string{"scope", "session.sqlite", "--diff", pythonMutationDiff, "--base", since}
	for _, op := range checks.SkippedMutators(checks.MutatorPython) {
		scopeArgs = append(scopeArgs, "--exclude-operator", op)
	}
	scoped, err := tk(inited.ctr.
		WithNewFile(pythonMutationStepOutput, "").
		WithEnvVariable("GITHUB_OUTPUT", pythonMutationStepOutput),
		scopeArgs...)
	if err != nil {
		return neverRan(err)
	}
	if scoped.code != 0 {
		// The diff's paths match no enumerated site: a green over zero mutants
		// is the failure this gate exists to close, and the declaration is the
		// star's to fix.
		return settle(1, fmt.Sprintf("forge-testkit-mutation scope exited %d — the diff's paths match no enumerated mutation site; check critical-modules against the tree\n%s", scoped.code, lastLines(scoped.out, 20)))
	}
	stepOutput, _ := scoped.ctr.File(pythonMutationStepOutput).Contents(ctx)
	if checks.PythonUnmutable(stepOutput) {
		return settle(0, "no mutable line in the diff — zero mutants ran")
	}

	plugged, err := tk(scoped.ctr, "plugin", "--out", mutationDir+"/plugin")
	if err != nil {
		return neverRan(err)
	}
	if plugged.code != 0 {
		return cannot("the test-selection plugin could not be written", plugged)
	}
	planned, err := tk(plugged.ctr, "plan", "session.sqlite", "--workers", strconv.Itoa(checks.PythonMutationWorkers))
	if err != nil {
		return neverRan(err)
	}
	workers, _, ok := checks.PythonPlan(planned.out)
	if planned.code != 0 || !ok {
		return cannot("the session could not be planned", planned)
	}

	// MEASURE, once per worker and concurrently, so the elapsed time carries
	// the contention the mutants will. FORGE_MUT_WORKER tells the engine the
	// runs apart; without it the identical execs would be run once.
	plugin := plugged.ctr.
		WithEnvVariable("PYTHONPATH", mutationDir+"/plugin").
		WithEnvVariable("PYTEST_ADDOPTS", "-p _forge_mutation_select")
	include := strings.Join(kept, " ")
	started := time.Now()
	measured, err := concurrently(workers, func(i int) (ran, error) {
		return do(plugin.
			WithEnvVariable("FORGE_MUT_WORKER", strconv.Itoa(i+1)).
			WithEnvVariable("PYTHONDONTWRITEBYTECODE", "1").
			WithEnvVariable("FORGE_MUT_MEASURE", mutationDir+"/measure").
			WithEnvVariable("FORGE_MUT_INCLUDE", include),
			append([]string{"uv", "run", "--with", checks.PythonWith("coverage")}, checks.PythonTestCommand...)...)
	})
	if err != nil {
		return neverRan(err)
	}
	elapsed := int(time.Since(started).Seconds())
	parts := plugin
	for i, m := range measured {
		if why := checks.PythonMeasureFailure(i+1, m.code, m.out); why != "" {
			return settle(2, why)
		}
		parts = parts.WithDirectory(mutationDir+"/measure", m.ctr.Directory(mutationDir+"/measure"))
	}
	mapped, err := tk(parts, "map", "--parts", mutationDir+"/measure", "--modules", include, "--root", "/src", "--out", mutationDir+"/map.json")
	if err != nil {
		return neverRan(err)
	}
	if mapped.code != 0 {
		return cannot("the test map could not be built", mapped)
	}

	// EXEC, on the session partitioned across the workers.
	selecting := mapped.ctr.
		WithNewFile(pythonMutationConfig, checks.CosmicRayConfig(kept, checks.PythonTestCommand, checks.PythonMutantTimeout(elapsed))).
		WithEnvVariable("FORGE_MUT_SELECT", mutationDir+"/map.json").
		WithEnvVariable("FORGE_MUT_SELECT_LOG", mutationDir+"/select.log")
	// Each worker's session is named once, where partition writes it and the
	// worker reads it.
	sessions := make([]string, workers)
	partition := []string{"partition", "session.sqlite", "--map", mutationDir + "/map.json"}
	for i := range workers {
		sessions[i] = fmt.Sprintf("%s/w%d.sqlite", mutationDir, i+1)
		partition = append(partition, "--out", sessions[i])
	}
	partitioned, err := tk(selecting, partition...)
	if err != nil {
		return neverRan(err)
	}
	if partitioned.code != 0 {
		return cannot(fmt.Sprintf("the session could not be partitioned across %d workers", workers), partitioned)
	}
	executed, err := concurrently(workers, func(i int) (ran, error) {
		return do(partitioned.ctr.
			WithEnvVariable("FORGE_MUT_WORKER", strconv.Itoa(i+1)).
			WithFile("/src/session.sqlite", partitioned.ctr.File(sessions[i])),
			"uv", "run", "cosmic-ray", "exec", pythonMutationConfig, "session.sqlite")
	})
	if err != nil {
		return neverRan(err)
	}
	status := 0
	merge := []string{"merge", "session.sqlite"}
	gathered := partitioned.ctr
	var selections strings.Builder
	for i, w := range executed {
		if status == 0 {
			status = w.code
		}
		part := fmt.Sprintf("%s/parts/w%d.sqlite", mutationDir, i+1)
		gathered = gathered.WithFile(part, w.ctr.File("/src/session.sqlite"))
		merge = append(merge, part)
		// A worker that selected nothing wrote no log.
		log, _ := w.ctr.File(mutationDir + "/select.log").Contents(ctx)
		selections.WriteString(log)
	}
	merged, err := tk(gathered.WithNewFile(mutationDir+"/select.log", selections.String()), merge...)
	if err != nil {
		return neverRan(err)
	}
	// A worker that left jobs unscored is a broken exec, whatever its own exit
	// said: the score must not read a sample nobody chose.
	if status == 0 {
		status = merged.code
	}
	if status != 0 {
		return settle(2, fmt.Sprintf("CANNOT RUN - cosmic-ray exec exited %d — a broken run, not a survivor report\n%s", status, lastLines(merged.out, 20)))
	}
	// Exit 3 is a mutant run that pruned its collection and then matched none
	// of its selected tests: its outcome is not a measurement. Any other
	// failure is a summary that could not be read, and exec's status stands.
	selection, err := tk(merged.ctr, "selection", mutationDir+"/select.log")
	if err != nil {
		return neverRan(err)
	}
	if selection.code == 3 {
		return cannot("a mutant run pruned its test collection and then matched none of its selected tests, so its outcome is not a measurement", selection)
	}

	// SCORE: the honest report, stdout and stderr apart.
	reported := merged.ctr.WithExec([]string{"uv", "run", "--no-project", "--isolated",
		"--with", checks.PythonMutationTestkit, "forge-testkit-mutation", "report", "session.sqlite", "--fail-under", strconv.Itoa(checks.PythonMutationFailUnder)}, anyExit)
	rc, err := reported.ExitCode(ctx)
	if err != nil {
		return neverRan(err)
	}
	stdout, err := reported.Stdout(ctx)
	if err != nil {
		return neverRan(err)
	}
	stderr, err := reported.Stderr(ctx)
	if err != nil {
		return neverRan(err)
	}
	return settle(checks.PythonReportVerdict(rc, stdout, stderr))
}

// concurrently runs f for 0..n-1 at once and answers the results in order, or
// the first error.
func concurrently[T any](n int, f func(i int) (T, error)) ([]T, error) {
	out := make([]T, n)
	g := new(errgroup.Group)
	for i := range n {
		g.Go(func() (err error) {
			out[i], err = f(i)
			return err
		})
	}
	return out, g.Wait()
}

// lastLines is the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

const (
	// pythonMutationConfig is cosmic-ray's config, at the checkout root where
	// its relative module-path resolves.
	pythonMutationConfig     = "cosmic-ray.toml"
	pythonMutationDiff       = mutationDir + "/pr.diff"
	pythonMutationStepOutput = mutationDir + "/scope.out"
)
