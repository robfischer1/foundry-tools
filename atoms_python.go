package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE PYTHON LANE, AS TYPED CHAINS. Read runtime.go's eight rules and
// atoms_go.go's exemplar first; this file follows both.
//
// pyright is deliberately absent from this file, not overlooked. Two type
// checkers ran on every python pre-push with no incident behind the
// duplication, and it is dropped fleet-wide (decided, Rob). mypy is the one.

func init() {
	register("python:ruff-check", pythonRuffCheck)
	register("python:ruff-format", pythonRuffFormat)
	// Three forge-testkit lint hooks, one function: they differ only by mode
	// and by the files that mode reads, and the pattern belongs beside the id.
	// No assertion-free test bodies.
	register("python:forge-testkit-assertion-free", forgeTestkitLint("assertion-free", "tests/**/*.py"))
	// Fake and Stub doubles live where they belong.
	register("python:forge-testkit-fake-placement", forgeTestkitLint("fake-placement", "**/*.py"))
	// MCP verb descriptions stay inside the schema budget.
	register("python:forge-testkit-schema-budget", forgeTestkitLint("schema-budget", "src/**/*.py"))
	register("python:mypy", pythonMypy)
	register("python:pytest", pythonPytest)
	register("python:pip-audit", pythonPipAudit)
	register("python:mutation", pythonMutation)
}

// ruffVersion pins the linter. A floating ruff is a gate whose verdict is not
// a function of the pin the door declared — the same argument images.go makes
// for the lane images, applied to the tool uvx fetches.
const ruffVersion = "ruff@0.16.3"

// pythonRuleset answers the CANNOT RUN a missing fleet ruleset is, reading
// foundry-stocks IN GO before any container starts.
//
// A RULESET THE ATOM CANNOT READ IS A GATE THAT NEVER LOOKED, and that is
// never a pass. The shell form was a `[ -f /stocks/... ]` test inside the
// container, which could only run after the mount had already been paid for;
// here the file is read off the tree the mount is made from, so the same
// question is asked one step earlier and the message is unchanged.
//
// HOISTABLE: ts:bun-gate and ts:bun-gate-commit ask this of
// eslint.config.mjs. Left unexported here rather than put in runtime.go so the
// lane ports do not collide; Tesla19 hoists it.
func pythonRuleset(ctx context.Context, r *run, a checks.AtomDef, file string) (checks.Verdict, bool) {
	if _, err := r.stocks.File("ci/lib/rulesets/" + file).Contents(ctx); err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+checks.RulesetsDir+"/"+file+
			" is absent; foundry-stocks did not mount at its one home, so the fleet's ruleset cannot be read."), false
	}
	return checks.Verdict{}, true
}

// pythonRootEntries is the repository root, or the CANNOT RUN that not being
// able to read it is. A tree the engine cannot list is a fact about the
// repository, not a finding about it.
func pythonRootEntries(ctx context.Context, r *run, a checks.AtomDef) ([]string, checks.Verdict, bool) {
	entries, err := r.src.Entries(ctx)
	if err != nil {
		return nil, checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - could not read the repository root: "+err.Error()), false
	}
	return entries, checks.Verdict{}, true
}

// ruff lint is clean over every .py in the tree, under the fleet's ruleset.
//
// THE RULESET IS THE FLEET'S. `--config <file>` makes ruff ignore every
// pyproject.toml and ruff.toml in the tree, so the repository's copy — the
// template pours one, and seven repos had drifted from it by 2026-09-11 —
// decides nothing here. foundry-stocks ci/lib/rulesets/ruff.toml is the
// template's ruleset with one home (Rob, 2026-09-11: the fleet decides the
// atoms AND their rulesets).
func pythonRuffCheck(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("python:ruff-check")
	if v, ok := pythonRuleset(ctx, r, a, "ruff.toml"); !ok {
		return v
	}
	return verdict(ctx, a, r.withStocks(r.lane(checks.ImagePython)).
		// The provisioning probe: uvx resolves and caches the pinned ruff, and
		// a resolver that could not is a could-not-run rather than a finding.
		WithExec([]string{"uvx", ruffVersion, "--version"}).
		WithExec([]string{"uvx", ruffVersion, "check", "--config", checks.RulesetsDir + "/ruff.toml", "."}, anyExit))
}

// ruff format --check is clean over the product python, under the fleet's
// ruleset.
//
// --check, NEVER the rewrite. The stock hook reformats in place and fails so
// you re-stage, which has aborted a commit in this fleet before (themis). The
// path scope is the measurement the go stars' hook records: incidental python
// (scripts/, conformance recorders) is linted but not formatted; product
// python under src/ and tests/ is — checks.RuffFormatTargets decides that, in
// Go, from the root entries. The width and the rest come from the fleet's
// ruff.toml, as above.
//
// THE RULESET IS ASKED FOR FIRST, before the absence, because that is the
// order the shell body asked in: a run that could not read the fleet's ruleset
// reports so even where it would have had nothing to format.
func pythonRuffFormat(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("python:ruff-format")
	if v, ok := pythonRuleset(ctx, r, a, "ruff.toml"); !ok {
		return v
	}
	entries, v, ok := pythonRootEntries(ctx, r, a)
	if !ok {
		return v
	}
	targets, ok := checks.RuffFormatTargets(entries)
	if !ok {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - a go star with no product python under src/ or tests/")
	}
	args := append([]string{"uvx", ruffVersion, "format", "--config", checks.RulesetsDir + "/ruff.toml", "--check"}, targets...)
	return verdict(ctx, a, r.withStocks(r.lane(checks.ImagePython)).
		WithExec([]string{"uvx", ruffVersion, "--version"}).
		WithExec(args, anyExit))
}

// forgeTestkitLint binds one mode and its file pattern to a runner. Three
// copies of a provisioning probe is exactly the duplication this repository
// exists to delete.
func forgeTestkitLint(mode, pattern string) atomFn {
	return func(ctx context.Context, r *run) checks.Verdict {
		return pythonForgeTestkit(ctx, r, mode, pattern)
	}
}

// pythonForgeTestkit runs one forge-testkit-lint pre-commit hook.
//
// THE HOOK TAKES PATHS. pre-commit appends the files its `files:` pattern
// matched, and the CLI refuses to run without them ("the following arguments
// are required: paths"). MEASURED 2026-09-10T01:25Z gate-helios-057545b:
// fake-placement and schema-budget red on that usage error in every repo the
// gate touched, go and python alike.
//
// THE PATTERNS ARE THE TEMPLATE HOOK'S `files:`, READ OVER THE GATE'S OWN
// POPULATION so the fleet exclude applies. The shell body spelled them as git
// pathspecs — `population -- 'tests/*.py'` — where an unanchored `*` crosses a
// `/`; the Dagger glob that asks the same question is `tests/**/*.py`, and
// likewise `**/*.py` for `*.py` and `src/**/*.py` for `src/*.py`. The
// population itself is the engine's gitignore filter plus checks.GateExclude,
// in Go, rather than `git ls-files | grep -v`.
//
// TWO ABSENCES, BOTH ANNOUNCED. No forge-testkit DEPENDENCY ENTRY in
// pyproject.toml is ABSENT — a repo that never took the dependency has nothing
// for the linter to read, and checks.DeclaresForgeTestkit says why a mention
// is not an entry. No matching file is ABSENT too: pre-commit skips a hook
// with an empty file list, and a linter handed nothing would be a pass over
// nothing.
//
// THE FILE LIST IS ARGUMENTS, NOT xargs. The shell body piped it through
// `xargs -r`, which is not available to a typed chain for a reason worth
// writing down: xargs answers 123 when the command it ran exited 1-125, so the
// linter's own exit 1 would reach the verdict as 123 and checks.StateFor would
// read a FINDING as a could-not-run. Rule 2 says the tool's exit code is the
// verdict, so the tool is the process — and no repo in custody has a python
// population anywhere near an argv (MEASURED 2026-09-11 across every clone in
// Forge/Outputs: the largest, foundry-stocks, tracks 114 .py files, 4,734
// bytes of path text against a 2 MiB ARG_MAX). If one ever does, the fix is
// the NUL-file-and-xargs form WITH a pure remap of 123, not a silent 123.
func pythonForgeTestkit(ctx context.Context, r *run, mode, pattern string) checks.Verdict {
	a := checks.AtomByID("python:forge-testkit-" + mode)

	pyproject, err := r.src.File("pyproject.toml").Contents(ctx)
	if err != nil || !checks.DeclaresForgeTestkit(pyproject) {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - forge-testkit is not a dependency of this project")
	}
	files, err := r.population(ctx, pattern)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - could not enumerate the repository: "+err.Error())
	}
	if len(files) == 0 {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - no files match "+pattern)
	}

	args := append([]string{"uv", "run", "--extra", "dev", "forge-testkit-lint", mode}, files...)
	return verdict(ctx, a, r.lane(checks.ImagePython).
		WithExec([]string{"uv", "--version"}).
		WithExec(args, anyExit))
}

// mypy is clean over src and tests, under the fleet's strict configuration.
//
// THE CONFIGURATION IS THE FLEET'S: --config-file makes mypy ignore the
// [tool.mypy] table in the repository's pyproject.toml. Strict is in the file,
// not the flag, so there is one place the rules live.
//
// --all-extras because a type check that cannot import the optional
// dependencies the code declares is a type check of a different program.
func pythonMypy(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("python:mypy")
	if v, ok := pythonRuleset(ctx, r, a, "mypy.ini"); !ok {
		return v
	}
	entries, v, ok := pythonRootEntries(ctx, r, a)
	if !ok {
		return v
	}
	targets := checks.PythonSourceDirs(entries)
	if len(targets) == 0 {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - no src/ or tests/ to type-check")
	}
	args := append([]string{"uv", "run", "--all-extras", "mypy", "--config-file", checks.RulesetsDir + "/mypy.ini"}, targets...)
	return verdict(ctx, a, r.withStocks(r.lane(checks.ImagePython)).
		WithExec([]string{"uv", "--version"}).
		WithExec(args, anyExit))
}

// pytest passes, and there is something for it to pass.
//
// NO TESTS IS A FINDING. This atom used to answer ABSENT — exit 0 — for a tree
// with no tests/ and for pytest's exit 5 ("no tests ran"), on the reasoning
// that a go star with one .py file (gate-helios-057545b, 2026-09-10) was in
// its permanent state, not at fault. Rob, 2026-09-11: nothing is built without
// tests. A python lane with nothing to collect is red, and the go star with a
// stray .py file is a repo that should not carry a pyproject.
//
// The emptiness is decided in Go, off the tree, before pytest is asked:
// no tests/ directory AND no file matching either of pytest's two default
// naming conventions. checks.PytestState carries the rest of the mapping,
// including the shell body's fold of every non-zero code that is not 5 to one
// finding.
func pythonPytest(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("python:pytest")

	entries, v, ok := pythonRootEntries(ctx, r, a)
	if !ok {
		return v
	}
	if !slices.Contains(checks.PythonSourceDirs(entries), "tests") {
		files, err := r.population(ctx, "**/test_*.py", "**/*_test.py")
		if err != nil {
			return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - could not enumerate the repository: "+err.Error())
		}
		if len(files) == 0 {
			return checks.VerdictOf(a, 1, a.ID+": FINDINGS - no tests/ and no test files; nothing is built without tests")
		}
	}

	// gitReady for the same reason go:test-race carries it: a suite that
	// shells out to git must find a repository, not a worktree's dangling
	// `.git` file.
	out, code, err := output(ctx, r.gitReady(ctx, r.lane(checks.ImagePython)).
		WithExec([]string{"uv", "--version"}).
		WithExec([]string{"uv", "run", "--all-extras", "pytest", "-q"}, anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	state, reason := checks.PytestState(code)
	if reason != "" {
		return checks.VerdictOf(a, state, a.ID+": FINDINGS - "+reason)
	}
	return checks.VerdictOf(a, state, out)
}

// pip-audit reports no known vulnerability.
func pythonPipAudit(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("python:pip-audit")
	return verdict(ctx, a, r.lane(checks.ImagePython).
		WithExec([]string{"uv", "--version"}).
		// --with rather than a dependency of the repo: the auditor is the
		// fleet's tool, and a repo that did not declare it is still audited.
		WithExec([]string{"uv", "run", "--with", "pip-audit", "pip-audit"}, anyExit))
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
	answers, _ := r.src.File(".copier-answers.yml").Contents(ctx)
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

	// rev-parse --verify --quiet, not cat-file -e: cat-file answers a missing
	// object with 128, which the engine reports as its own error even under
	// Expect ANY (foundry-tools#63).
	_, code, err := output(ctx, ctr.WithExec([]string{"git", "rev-parse", "--verify", "--quiet", r.base + "^{commit}"}, anyExit))
	if err != nil {
		return neverRan(err)
	}
	if code != 0 {
		return settle(0, noBase)
	}

	// RESOLVE: the declared modules, or every python source the pull added or
	// changed outside the tests; less the generated ones.
	modules := strings.Fields(mods)
	if len(modules) == 0 {
		out, code, err := output(ctx, ctr.WithExec(append([]string{"git", "diff", "--name-only", "--diff-filter=AM", r.base, "HEAD", "--"}, checks.PythonWholeDiffSpecs...), anyExit))
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
	diff, code, err := output(ctx, ctr.WithExec(append([]string{"git", "diff", "--unified=0", r.base, "HEAD", "--"}, kept...), anyExit))
	if err != nil {
		return neverRan(err)
	}
	if code != 0 {
		return settle(2, "CANNOT RUN - git could not diff the pull against its base "+r.base+": "+diff)
	}
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
		return do(ctr, append([]string{"uv", "run", "--no-project", "--isolated", "--index", checks.PythonMutationIndex,
			"--with", checks.PythonMutationTestkit, "forge-testkit-mutation"}, args...)...)
	}
	cannot := func(what string, step ran) checks.Verdict {
		return settle(2, fmt.Sprintf("CANNOT RUN - %s (exit %d)\n%s", what, step.code, lastLines(step.out, 20)))
	}

	inited, err := do(synced.ctr.
		WithNewFile(pythonMutationConfig, checks.CosmicRayConfig(kept, checks.PythonTestCommand, checks.PythonMutationTimeout)).
		WithNewFile(pythonMutationDiff, diff+"\n"),
		"uv", "run", "--with", "cosmic-ray", "cosmic-ray", "init", pythonMutationConfig, "session.sqlite")
	if err != nil {
		return neverRan(err)
	}
	if inited.code != 0 {
		return cannot("cosmic-ray init enumerated no mutation sites", inited)
	}

	// SCOPE: drop the out-of-diff jobs. The base rides along (testkit 1.8.0),
	// so an added line inside a unit whose AST is unchanged — a reformat —
	// leaves the scope too.
	scoped, err := tk(inited.ctr.
		WithNewFile(pythonMutationStepOutput, "").
		WithEnvVariable("GITHUB_OUTPUT", pythonMutationStepOutput),
		"scope", "session.sqlite", "--diff", pythonMutationDiff, "--base", r.base)
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
			append([]string{"uv", "run", "--with", "coverage>=7.4"}, checks.PythonTestCommand...)...)
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
			"uv", "run", "--with", "cosmic-ray", "cosmic-ray", "exec", pythonMutationConfig, "session.sqlite")
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
	reported := merged.ctr.WithExec([]string{"uv", "run", "--no-project", "--isolated", "--index", checks.PythonMutationIndex,
		"--with", checks.PythonMutationTestkit, "forge-testkit-mutation", "report", "session.sqlite", "--fail-under", "0"}, anyExit)
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
