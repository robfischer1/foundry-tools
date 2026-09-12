package main

import (
	"context"
	"slices"

	"dagger/foundry-tools/internal/checks"
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

	out, code, err := output(ctx, r.lane(checks.ImagePython).
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

// mutationPhasesPython are the phases of the canonical python mutation script,
// in the order it expects them.
var mutationPhasesPython = []string{"resolve", "sync", "config", "init", "scope", "exec", "score"}

// Every mutant cosmic-ray makes of this pull's changes to the declared
// critical modules is killed by the tests.
//
// ONE SHAPE, FOUR LANGUAGES. This runs the canonical script at its one home
// (/stocks/ci/lib/mutation/python.sh) phase by phase, in DIFF mode against
// GATE_BASE — the pull's merge base as the door names it — and answers with
// the verdict the score phase wrote: 0 clean, 1 survivors, 2 could not
// measure. The phases themselves never exit non-zero (reaching a verdict is
// the score phase's job), so a phase that does is a broken script, said as
// CANNOT RUN and naming the phase.
//
// THE HISTORY IS THERE IN THE LANE THAT MATTERS. The mutation Job clones the
// repository whole and checks the head out, so `git cat-file -e <base>`
// answers and the diff is real. A local pre-push run hands the engine a linked
// worktree, which gitReady turns into a throwaway repository with no history:
// the resolve phase then stands down 0 with "no usable PR base sha", printed,
// and the door's Job is the one that measures.
//
// critical_modules IS THE REPO'S DECLARATION, read off .copier-answers.yml
// where the template question puts it — the same string the retired
// mutation.yml rendered into its `modules` input. A repo has no other say: the
// first cut sourced a repo-root ci/mutation.env of MUT_* knobs, and Rob asked
// why a repo should have a say in anything (2026-09-11). It should not — the
// scripts honour MUT_GATE=false, so that file was a one-line switch to turn a
// fleet gate off. An empty declaration is the whole diff, not an opt-out, and
// checks.MutationScope prints which of the two happened.
//
// GATE_BASE reaches this atom, and almost no other: withBase is what rule 8
// restricts, so every atom that does not judge the change keeps a cache key
// that is a function of the tree alone.
func pythonMutation(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("python:mutation")
	const script = "ci/lib/mutation/python.sh"

	if _, err := r.stocks.File(script).Contents(ctx); err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - /stocks/"+script+
			" is absent; foundry-stocks did not mount at its one home.")
	}
	// Absent answers file, absent declaration: "" is the whole diff.
	answers, _ := r.src.File(".copier-answers.yml").Contents(ctx)
	mods := checks.CriticalModules(answers)
	scope := checks.MutationScope(a.ID, mods)

	ctr := r.gitReady(ctx, r.withBase(r.withStocks(r.lane(checks.ImagePython)))).
		WithExec([]string{"bash", "--version"}).
		WithExec([]string{"uv", "--version"}).
		WithEnvVariable("MUT_DIR", "/tmp/mutation").
		WithEnvVariable("MUT_MODE", "diff").
		WithEnvVariable("MUT_BASE", r.base).
		WithEnvVariable("MUT_MODULES", mods)

	// ONE EXEC PER PHASE, and each is asked its code before the next is
	// built: the phase's name is half the message, and a chain that failed
	// somewhere cannot say where.
	for _, phase := range mutationPhasesPython {
		ctr = ctr.WithExec([]string{"bash", "/stocks/" + script, phase}, anyExit)
		out, code, err := output(ctx, ctr)
		if err != nil {
			return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
		}
		if code != 0 {
			return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - phase "+phase+
				" exited non-zero; the phases never do on their own\n"+out)
		}
	}

	verdictFile, _ := ctr.File("/tmp/mutation/verdict").Contents(ctx)
	reasonFile, _ := ctr.File("/tmp/mutation/reason").Contents(ctx)
	state, reason, err := checks.MutationVerdict(verdictFile, reasonFile)
	if err != nil {
		return checks.VerdictOf(a, 2, scope+"\n"+a.ID+": CANNOT RUN - "+err.Error())
	}
	return checks.VerdictOf(a, state, scope+"\n"+a.ID+": "+reason)
}
