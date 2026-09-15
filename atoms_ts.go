package main

import (
	"context"

	"dagger/foundry-tools/internal/checks"
)

// THE TS LANE, AS TYPED CHAINS. Read runtime.go's eight rules and atoms_go.go's
// exemplar first.
//
// lint-staged is deliberately absent, for the reason checks_ts.go states: it is
// defined over the git INDEX and the engine receives a directory.

func init() {
	register("ts:bun-gate-commit", tsBunGateCommit)
	register("ts:bun-gate", tsBunGate)
	register("ts:bun-audit", tsBunAudit)
	register("ts:mutation", tsMutation)
}

// eslintConfig is the fleet's eslint config inside the foundry-stocks tree.
const eslintConfig = "ci/lib/rulesets/eslint.config.mjs" // under checks.StocksRulesets; spelled out so no package-level `+` sits uncovered

// bun run gate (format, lint, typecheck, test, build) passes under the fleet's
// eslint config.
func tsBunGateCommit(ctx context.Context, r *run) checks.Verdict {
	return tsGate(ctx, r, "ts:bun-gate-commit", false)
}

// bun run gate passes against a frozen lockfile under the fleet's eslint
// config, and the tree carries tests for it to run.
func tsBunGate(ctx context.Context, r *run) checks.Verdict {
	return tsGate(ctx, r, "ts:bun-gate", true)
}

// tsGate is both bun-gate atoms: they differ only in whether the tree is
// required to carry a test file, so they are one function and a flag rather
// than two bodies free to drift.
//
// THE GATE'S CHECKOUT HAS NO node_modules. The pre-commit hook this ports runs
// in a working tree that already installed; the engine mounts a bare tree.
// MEASURED 2026-09-10T02:38Z gate-calliope-9ed7599: `bun run format:check` →
// "prettier: command not found", exit 127, a red about the runner and not
// about the repo. So the install is part of the atom, and a lockfile that will
// not install frozen is a CANNOT RUN — it is a fact about the pull's
// reproducibility, and reading it as a finding would put it in the wrong
// queue. (A 127 that survives the install now reaches the verdict as a
// could-not-run on its own, where the shell body's `|| exit 1` flattened it
// into FINDINGS. That is the same story's correct ending.)
//
// THE ESLINT CONFIG IS THE FLEET'S, WRITTEN OVER THE REPOSITORY'S. eslint
// resolves its plugins relative to the config file, so a config outside the
// tree cannot load them; the fleet's file is placed at the root before the
// gate runs, and every package's `eslint .` finds it there (the template pours
// one root config and no per-package one). What the repo's copy said decides
// nothing (Rob, 2026-09-11) — overwriting it is the point, not a side effect.
//
// It is written into the DIRECTORY and the tree re-mounted, rather than laid
// over the existing mount with Container.WithFile: /src is a mount, and
// gitReady already establishes that re-mounting is how this runtime edits it.
func tsGate(ctx context.Context, r *run, id string, requireTests bool) checks.Verdict {
	a := checks.AtomByID(id)

	// A ruleset the atom cannot read is a gate that never looked, and that is
	// never a pass. Decided in Go, off the stocks tree, before anything runs.
	cfg := r.stocks.File(eslintConfig)
	if _, err := cfg.Contents(ctx); err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+checks.RulesetsDir+"/eslint.config.mjs is absent; foundry-stocks did not mount at its one home, so the fleet's ruleset cannot be read.\n"+err.Error())
	}

	ctr := r.lane(checks.ImageTS).
		WithMountedDirectory("/src", r.src.WithFile("eslint.config.mjs", cfg))

	installed := ctr.WithExec([]string{"bun", "install", "--frozen-lockfile"}, anyExit)
	out, code, err := output(ctx, installed)
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	if code != 0 {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - frozen lockfile install failed\n"+out)
	}

	// NO TESTS IS A FINDING, and the presence check is the atom's own rather
	// than bun's: `bun test` refuses a tree with no test file, but this atom
	// runs the REPO'S `gate` script, which may never reach `bun test` at all.
	// Rob, 2026-09-11: nothing is built without tests. The population is the
	// gate's own — the engine's gitignore filter and the fleet exclude, which
	// is what the shell body's `-path ./node_modules -prune` was doing by hand.
	//
	// The order is the old body's: the install runs first, so a tree that
	// cannot install frozen answers CANNOT RUN rather than being graded on its
	// test files. A population that cannot be enumerated is now a could-not-run
	// too, where `find ... 2>/dev/null` read a broken scan as an empty tree.
	if requireTests {
		tests, err := r.population(ctx, checks.TSTestPatterns...)
		if err != nil {
			return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the tree could not be enumerated, so its test surface is unknown rather than empty: "+err.Error())
		}
		if len(tests) == 0 {
			return checks.VerdictOf(a, 1, a.ID+": FINDINGS - no test file in the tree (bun's pattern: {.test,.spec,_test_,_spec_}.{js,ts,jsx,tsx}); nothing is built without tests")
		}
	}

	return verdict(ctx, a, installed.WithExec([]string{"bun", "run", "gate"}, anyExit))
}

// bun audit reports nothing at high or above.
//
// THE REGISTRY IS NAMED, AND IT IS npmjs.org. The override was in this atom's
// body from the day it was written and it carried no comment (checked back to
// d15427c, the commit that introduced the 28 atoms) — so the REASONING here is
// reconstructed from the mechanism, not quoted from a measurement. `bun audit`
// scans nothing locally: it posts the lockfile's package set to the registry's
// bulk advisory endpoint and reports what comes back. A proxy mirror serves
// packages, not that API, and the fleet mirrors its package managers through
// Nexus wherever it can (measured for cargo — checks.CachesFor leaves
// /usr/local/cargo/config.toml uncovered precisely so the Nexus route
// survives). Against a mirror this atom would ask a host that cannot answer
// and report a clean audit it never performed, which is the zero-file scan
// this module exists to delete. Naming the registry for this one command
// points the question at the host that can answer it; nothing is installed
// here, so no mirror's job is affected.
//
// --audit-level=high is the fleet's threshold: bun exits 1 when something at or
// above it is reported, and that 1 is the finding.
func tsBunAudit(ctx context.Context, r *run) checks.Verdict {
	return verdict(ctx, checks.AtomByID("ts:bun-audit"),
		r.lane(checks.ImageTS).
			WithEnvVariable("BUN_CONFIG_REGISTRY", "https://registry.npmjs.org/").
			WithExec([]string{"bun", "audit", "--audit-level=high"}, anyExit))
}

// Every mutant StrykerJS makes of this pull's changes to the declared critical
// modules is killed by the tests. scriptedMutation carries the shape's reasoning.
func tsMutation(ctx context.Context, r *run) checks.Verdict {
	return scriptedMutation(ctx, r, mutationSpec{
		id:     "ts:mutation",
		image:  checks.ImageTS,
		script: "ci/lib/mutation/ts.sh",
		probes: [][]string{{"bash", "--version"}, {"bun", "--version"}},
		phases: []string{"resolve", "install", "build", "mutate", "score"},
	})
}

// mutationSpec is what a mutation atom that still runs a foundry-stocks script
// names: the image, the canonical script, what has to be on the PATH for it,
// and the phases that script defines.
type mutationSpec struct {
	id     string
	image  string
	script string // relative to the foundry-stocks tree
	probes [][]string
	phases []string
}

// scriptedMutation is the shape ts:mutation still runs. go: and rust: left it
// for plain execs settled in Go (checks.GoMutationVerdict,
// checks.RustMutationVerdict); python: carries its own copy of it. Both
// scripted atoms read the score phase's two files through one
// checks.MutationVerdict and print one checks.MutationScope line.
//
// It runs the canonical script at its one home (/stocks/ci/lib/mutation/<lang>.sh)
// PHASE BY PHASE, in DIFF mode against GATE_BASE — the pull's merge base as the
// door names it — and answers with the verdict the score phase wrote: 0 clean,
// 1 survivors, 2 could not measure. The phases themselves never exit non-zero
// (reaching a verdict is the score phase's job), so a phase that does is a
// broken script, said as CANNOT RUN and naming the phase. That is why each
// phase is its own evaluated exec rather than one chain read at the end: a
// chain would say only that something failed.
//
// THE HISTORY IS THERE IN THE LANE THAT MATTERS. The mutation Job clones the
// repository whole and checks the head out, so `git cat-file -e <base>`
// answers and the diff is real. A local pre-push run hands the engine a linked
// worktree, which gitReady turns into a throwaway repository with no history:
// the resolve phase then stands down 0 with "no usable PR base sha", printed,
// and the door's Job is the one that measures.
//
// A REPO HAS NO SAY. The first cut of these atoms sourced a repo-root
// ci/mutation.env — MUT_* knobs standing in for the retired workflow's inputs —
// and Rob asked why a repo should have a say in anything (2026-09-11). It
// should not: the scripts honour MUT_GATE=false, so that file was a one-line
// switch to turn a fleet gate off, the exact shape stop-justifications exists
// to refuse. It is gone. The one repo fact the lane reads is critical_modules,
// in the answers file the template question put it in — a declaration of WHAT
// matters, not a dial on HOW hard to look. Everything else is the fleet's
// default, here.
//
// GATE_BASE AND MUT_BASE ARE BOTH SET, and this is one of the two atoms
// runtime.go's rule 8 allows to read the base at all: every other atom's cache
// key stays a function of the tree rather than of the pull.
func scriptedMutation(ctx context.Context, r *run, s mutationSpec) checks.Verdict {
	a := checks.AtomByID(s.id)

	// The script IS the tool, read at its one home. Absent means foundry-stocks
	// did not mount, which is a could-not-run about the engine, not the repo.
	if _, err := r.stocks.File(s.script).Contents(ctx); err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - /stocks/"+s.script+" is absent; foundry-stocks did not mount at its one home.\n"+err.Error())
	}

	// critical_modules is read in Go off the tree, before any container runs.
	// A missing answers file is an empty declaration, exactly as the old
	// body's `2>/dev/null` made it.
	answers, _ := r.src.File(".copier-answers.yml").Contents(ctx)
	mods := checks.CriticalModules(answers)
	scope := checks.MutationScope(a.ID, mods)

	ctr := r.gitReady(ctx, r.withBase(r.withStocks(r.lane(s.image))))
	for _, probe := range s.probes {
		// Provisioning, under the default Expect: a missing toolchain is a
		// Dagger error and verdict() files it as state 2.
		ctr = ctr.WithExec(probe)
	}
	ctr = ctr.
		WithEnvVariable("MUT_DIR", mutDir).
		WithEnvVariable("MUT_MODE", "diff").
		WithEnvVariable("MUT_BASE", r.base).
		WithEnvVariable("MUT_MODULES", mods)

	for _, phase := range s.phases {
		next := ctr.WithExec([]string{"bash", "/stocks/" + s.script, phase}, anyExit)
		out, code, err := output(ctx, next)
		if err != nil {
			return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
		}
		if code != 0 {
			return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - phase "+phase+" exited non-zero; the phases never do on their own\n"+out)
		}
		ctr = next
	}

	raw, _ := ctr.File(mutDir + "/verdict").Contents(ctx)
	reason, _ := ctr.File(mutDir + "/reason").Contents(ctx)
	state, line, err := checks.MutationVerdict(raw, reason)
	if err != nil {
		return checks.VerdictOf(a, 2, scope+"\n"+a.ID+": CANNOT RUN - "+err.Error())
	}
	return checks.VerdictOf(a, state, scope+"\n"+a.ID+": "+line)
}

// mutDir is where the canonical scripts are told to keep their working state
// and to write the verdict and the reason the atom answers with.
const mutDir = "/tmp/mutation"
