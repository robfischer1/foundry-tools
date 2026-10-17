// Package tsbungate is the atom ts:bun-gate: bun run gate passes against a
// frozen lockfile under the fleet's eslint config, and the tree carries tests
// for it to run.
package tsbungate

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/rulesets"
)

// Def is the atom. It writes the fleet's eslint config at the tree's root and
// installs the tree's packages, so it writes the tree.
var Def = atomkit.Def{
	ID: "ts:bun-gate", Stage: checks.StagePrecommit, Lane: checks.LaneTS,
	Group:      checks.GroupLanguage,
	Desc:       "bun run gate passes against a frozen lockfile under the fleet's eslint config, and the tree carries tests for it to run.",
	Surface:    "root:package.json",
	Toolchain:  "bun",
	Needs:      []string{"tool:node"},
	Timeout:    30 * time.Minute,
	WritesTree: true,
	Check:      check,
}

const id = "ts:bun-gate"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneTS}

// eslintConfig is the config file the fleet lays at the root.
const eslintConfig = "eslint.config.mjs"

// check is the bun gate. It was two atoms (a commit-stage twin that skipped the
// test-file check) that ran the same `bun run gate` on the same trees: over
// 5,295 gated trees one reported findings on 72 and the other on 70. Rob,
// 2026-09-17: merge them.
//
// THE GATE'S CHECKOUT HAS NO node_modules. The pre-commit hook this ports runs
// in a working tree that already installed; the atom's checkout is bare
// (measured 2026-09-10, gate-calliope-9ed7599: `bun run format:check` ->
// "prettier: command not found", exit 127). So the install is part of the atom,
// and a lockfile that will not install frozen is a CANNOT RUN: it is a fact
// about the pull's reproducibility, and reading it as a finding would put it in
// the wrong queue.
//
// THE ESLINT CONFIG IS THE FLEET'S, WRITTEN OVER THE REPOSITORY'S. eslint
// resolves its plugins relative to the config file, so a config outside the tree
// cannot load them; the fleet's file is placed at the root before the gate runs,
// and every package's `eslint .` finds it there. What the repo's copy said
// decides nothing (Rob, 2026-09-11): overwriting it is the point.
//
// NO TESTS IS A FINDING, and the presence check is the atom's own rather than
// bun's: `bun test` refuses a tree with no test file, but this atom runs the
// REPO'S `gate` script, which may never reach `bun test` at all. Rob,
// 2026-09-11: nothing is built without tests. The order is the old body's: the
// install runs first, so a tree that cannot install frozen answers CANNOT RUN
// rather than being graded on its test files. The population is the one the atom
// was handed, read before the install wrote anything.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if err := os.WriteFile(filepath.Join(in.Root, eslintConfig), []byte(rulesets.ESLint), 0o644); err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the fleet's eslint config could not be laid: "+err.Error())
	}
	out, code, bad := atoms.RunTool(ctx, in, row, atoms.Cmd{Dir: in.Root, Name: "bun", Args: []string{"install", "--frozen-lockfile"}})
	if bad != nil {
		return *bad
	}
	if code != 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - frozen lockfile install failed\n"+out)
	}

	if in.FilesErr != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the tree could not be enumerated, so its test surface is unknown rather than empty: "+in.FilesErr.Error())
	}
	if !hasTests(in.Files) {
		return checks.VerdictOf(row, int(checks.StateFindings), id+": FINDINGS - no test file in the tree (bun's pattern: {.test,.spec,_test_,_spec_}.{js,ts,jsx,tsx}); nothing is built without tests")
	}

	out, code, bad = atoms.RunTool(ctx, in, row, atoms.Cmd{Dir: in.Root, Name: "bun", Args: []string{"run", "gate"}})
	if bad != nil {
		return *bad
	}
	// The script's exit is its failing tool's: tsc's 2 is a finding, not a
	// could-not-run (checks.BunGateExit says why).
	return checks.VerdictOf(row, checks.BunGateExit(code), out)
}

// hasTests reports whether the population holds a file bun would run as a test.
func hasTests(files []string) bool {
	for _, p := range checks.TSTestPatterns {
		if len(atoms.Matching(files, p)) > 0 {
			return true
		}
	}
	return false
}
