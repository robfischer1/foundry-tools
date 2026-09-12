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
const eslintConfig = checks.StocksRulesets + "/eslint.config.mjs"

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
// modules is killed by the tests. rustTSMutation carries the shape's reasoning.
func tsMutation(ctx context.Context, r *run) checks.Verdict {
	return rustTSMutation(ctx, r, mutationSpec{
		id:     "ts:mutation",
		image:  checks.ImageTS,
		script: "ci/lib/mutation/ts.sh",
		probes: [][]string{{"bash", "--version"}, {"bun", "--version"}},
		phases: []string{"resolve", "install", "build", "mutate", "score"},
	})
}
