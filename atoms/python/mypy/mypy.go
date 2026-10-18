// Package pythonmypy is the atom python:mypy: mypy is clean over src and tests,
// under the fleet's strict configuration.
package pythonmypy

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/rulesets"
)

// Def is the atom. A TYPE CHECK IS AGAINST A DEPENDENCY SET, and only the
// manifest declares one: a tree with no pyproject.toml has nothing installed,
// so every third-party import is unresolvable and mypy reports on a program
// that does not exist (measured on foundry-dies, 2026-09-25). The atom waits for
// the manifest; lint reads files and needs no imports, and pytest brings what
// it needs.
var Def = atomkit.Def{
	ID: "python:mypy", Stage: checks.StagePrecommit, Lane: checks.LanePython,
	Group:         checks.GroupLanguage,
	Desc:          "mypy is clean over src and tests, under the fleet's strict configuration.",
	Surface:       "root:pyproject.toml",
	Toolchain:     "python",
	Needs:         []string{"tool:uv"},
	Timeout:       30 * time.Minute,
	NotOnForks:    checks.ForkFleetStyle,
	NeedsManifest: true,
	WritesTree:    true,
	Check:         check,
}

const id = "python:mypy"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LanePython}

// check type-checks the product dirs under the fleet's strict configuration.
//
// THE CONFIGURATION IS THE FLEET'S: --config-file makes mypy ignore the
// [tool.mypy] table in the repository's pyproject.toml. Strict is in the file,
// not the flag, so there is one place the rules live. --all-extras (uv run's
// form for a project) because a type check that cannot import the optional
// dependencies the code declares is a type check of a different program.
//
// The targets are the directories that CARRY python, not the ones named src and
// tests: a .py-declared lane reaches Go stars whose src/ and tests/ are full of
// .go, and mypy pointed at one answers exit 2.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	entries, stop := atoms.PythonRootEntries(row, in)
	if stop != nil {
		return *stop
	}
	pys, stop := atoms.PythonTree(row, in)
	if stop != nil {
		return *stop
	}
	targets := checks.DirsCarryingPython(checks.PythonSourceDirs(entries), pys)
	if len(targets) == 0 {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - no src/ or tests/ carrying python to type-check")
	}
	cfg, err := atoms.WriteRuleset("mypy.ini", rulesets.Mypy)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the fleet's ruleset could not be written: "+err.Error())
	}
	if bad := atoms.Probe(ctx, in, row, "uv", "--version"); bad != nil {
		return *bad
	}
	c := atoms.UvCmd(in, entries, "mypy", "--config-file", cfg)
	c.Args = append(c.Args, targets...)
	return atoms.SettleTool(ctx, in, row, c)
}
